// k6 负载脚本 —— 三个场景，各自回答一个问题。
//
//   SCENARIO=steady  多航班读写 20:1 混合，恒定 500 QPS
//                    → 平峰下 R2/R3 成立吗（design § 1.4）
//   SCENARIO=read    打散到 50 个航班，只压 GET /flights/{id}
//                    → 读路径 2000–3000 QPS 这个推算对吗（design § 1.3）
//   SCENARIO=write   锁死 1 个航班，只压 POST /bookings
//                    → 行锁上限真是 300–1000 单/s 吗（design § 1.3、§ 5.1）
//
// read / write 两个阶梯场景各有两种发压模式，必须按顺序跑：
//
//   MODE=recon   ramping-vus（闭环）  → 吞吐在哪一档不再涨 = X_max
//   MODE=ladder  ramping-arrival-rate（开环）→ 超过 X_max 之后延迟怎么恶化
//
//   先 recon 拿到 X_max，再 `-e RATE_MAX=<1.5*X_max>` 跑 ladder。
//   闭环下 VU数 = 吞吐 × 延迟 是恒等式，饱和后延迟随 VU 严格线性增长，
//   所以闭环给得出吞吐平台，给不出过载后的非线性恶化（conventions/testing.md § 7）。
//
// 前置：make loadtest-seed —— 压测航班不在迁移里，见 k6/loadtest-seed.sql。
//
// 用法见 Makefile 的 loadtest-* 目标。

import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';
import { Counter } from 'k6/metrics';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';
import { textSummary } from 'https://jslib.k6.io/k6-summary/0.0.2/index.js';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const SCENARIO = __ENV.SCENARIO || 'steady';
const MODE = __ENV.MODE || 'ladder';
const RUN_NAME = __ENV.RUN_NAME || `${SCENARIO}-${MODE}`;

// ---------------------------------------------------------------------------
// 压测航班的确定性 ID —— 与 k6/loadtest-seed.sql 一一对应。
// ---------------------------------------------------------------------------
const READ_FLIGHT_COUNT = 50;
const READ_FLIGHTS = Array.from({ length: READ_FLIGHT_COUNT }, (_, i) =>
  `00000000-0000-4000-8000-${(i + 1).toString(16).padStart(12, '0')}`,
);
// 库存 5,000,000，压不干 —— 行锁竞争的是那一行，不是那个数字。
const HOT_FLIGHT = '00000000-0000-4000-8000-00000000ffff';
const LOADTEST_ROUTE = { origin: 'AAA', destination: 'BBB' };

// ---------------------------------------------------------------------------
// 409 不算失败。
//
// 座位不足 / 订单已取消都是 409，而它们是「正常业务结果，不是故障」
// （design § 3.3；R3 明写错误率不含正常业务拒绝）。把 409 留在 http_req_failed
// 里，就会在压测侧犯下 D-02 在 SLI 侧犯的同一个错误。
//
// 其余 4xx 仍然算失败 —— 那意味着脚本自己发错了请求。
// ---------------------------------------------------------------------------
http.setResponseCallback(http.expectedStatuses({ min: 200, max: 399 }, 409));

// ---------------------------------------------------------------------------
// 按档聚合：k6 自己算，不落 CSV。
//
// k6 的 `--out csv` 按「指标采样」写行而不是按请求写行——实测一个请求写 15 行
// （duration / waiting / sending / receiving / blocked / connecting / tls /
// failed / http_reqs / iterations / iteration_duration / checks×2 / data_sent /
// data_received），每行还把整套标签重复一遍，URL 出现两次。约 1.76 KB/请求：
// 读路径一次 4 分钟的跑就是 8.4 GB，而其中真正被用到的不到 2%。
//
// 做法：给每个请求打上它所属的阶梯档号，再把 `http_req_duration{step:N}` 这组
// 子指标声明成 threshold —— k6 只有见到声明才会去算子指标。于是百分位由 k6
// 内部算完，handleSummary 直接拿到，产出从 GB 级降到几 KB。
//
// 状态码分类单独用一个 Counter：卖光的签名未必是 409（409 洪水会把熔断器打开，
// 之后是 503，见 D-01），只看错误率会看漏「这一档根本没测到锁」。
// ---------------------------------------------------------------------------
const stepStatus = new Counter('lt_step_status');

function toMillis(text) {
  const m = /^(?:(\d+)m)?(?:([\d.]+)s)?$/.exec(text);
  if (!m) throw new Error(`无法解析时长: ${text}`);
  return (Number(m[1] || 0) * 60 + Number(m[2] || 0)) * 1000;
}

// 当前请求落在第几档。爬坡段返回 -1 —— 那段横跨两个负载水平，混进来会把
// 延迟摊平，稳态数只能取平台段。
function currentStep() {
  const cycle = toMillis(RAMP_DURATION) + toMillis(STEP_DURATION);
  const elapsed = exec.instance.currentTestRunDuration;
  const idx = Math.floor(elapsed / cycle);
  const within = elapsed - idx * cycle;
  return within < toMillis(RAMP_DURATION) ? -1 : idx;
}

// 档号必须在**发请求前**取（要当标签打进 http_req_duration），状态码分类只能在
// **收到响应后**记。所以是两个函数，不是一个。
function stepTag(endpoint) {
  const idx = currentStep();
  const tags = { endpoint };
  if (idx >= 0 && idx < LADDER_TARGETS.length) tags.step = String(idx);
  return tags;
}

function recordStatus(tags, res) {
  if (tags.step === undefined) return; // 爬坡段不计
  const cls =
    res.status === 409 ? 'biz409' : res.status >= 200 && res.status < 400 ? 'ok' : 'err';
  stepStatus.add(1, { step: tags.step, class: cls });
}

// ---------------------------------------------------------------------------
// 阶梯定义
// ---------------------------------------------------------------------------
const STEP_DURATION = __ENV.STEP_DURATION || '45s';
const RAMP_DURATION = __ENV.RAMP_DURATION || '10s';

// 每档的目标值。ladder 模式是 req/s，recon 模式是 VU 数。
function ladderSteps() {
  const max = Number(__ENV.RATE_MAX || defaultRateMax());
  // 自 0.5*max 铺到 1.5*max —— recon 测出的 X_max 通常略高于开环下的真实
  // 容量（高并发的调度开销在闭环里被 VU 自己吸收了），所以上界取 1.5 倍，
  // 保证拐点两侧都有采样点。
  const lo = max * 0.5;
  const hi = max * 1.5;
  const n = Number(__ENV.STEPS || 6);
  return Array.from({ length: n }, (_, i) =>
    Math.round(lo + ((hi - lo) * i) / (n - 1)),
  );
}

function reconSteps() {
  const max = Number(__ENV.VU_MAX || (SCENARIO === 'write' ? 200 : 400));
  const n = Number(__ENV.STEPS || 6);
  return Array.from({ length: n }, (_, i) => Math.round((max * (i + 1)) / n));
}

function defaultRateMax() {
  // 只在没跑过 recon 时兜底，取自 design § 1.3 的推算 —— 那是待验证的假设，
  // 不是依据。正式跑必须先 recon 再用 -e RATE_MAX 覆盖。
  return SCENARIO === 'write' ? 600 : 2000;
}

function stagesFrom(targets) {
  const stages = [];
  for (const t of targets) {
    stages.push({ duration: RAMP_DURATION, target: t });
    stages.push({ duration: STEP_DURATION, target: t });
  }
  return stages;
}

// 阶梯只算一次，运行期打标签和收尾出表都用它 —— 算两遍就会漂。
const IS_LADDER_SCENARIO = SCENARIO !== 'steady';
const LADDER_TARGETS = IS_LADDER_SCENARIO
  ? MODE === 'recon'
    ? reconSteps()
    : ladderSteps()
  : [];

// ---------------------------------------------------------------------------
// 场景配置
// ---------------------------------------------------------------------------
const STEADY_RATE = Number(__ENV.RATE || 500);
const STEADY_DURATION = __ENV.DURATION || '60s';

function buildScenario() {
  if (SCENARIO === 'steady') {
    // 恒定速率而非闭环 VU：闭环的实际吞吐恒等于 VU数 ÷ 延迟，凑不出一个稳定的
    // 「约 500 QPS」。这个场景要在**确定的** 500 QPS 下检查 SLO 成不成立，
    // 速率必须钉死。它不找拐点，所以闭环的缺陷在这里不适用。
    return {
      steady: {
        executor: 'constant-arrival-rate',
        rate: STEADY_RATE,
        timeUnit: '1s',
        duration: STEADY_DURATION,
        preAllocatedVUs: Math.max(50, Math.ceil(STEADY_RATE / 5)),
        maxVUs: Math.max(200, STEADY_RATE),
        exec: 'steadyMix',
      },
    };
  }

  const fn = SCENARIO === 'write' ? 'hotFlightWrite' : 'spreadRead';

  if (MODE === 'recon') {
    return {
      recon: {
        executor: 'ramping-vus',
        startVUs: 1,
        stages: stagesFrom(LADDER_TARGETS),
        gracefulRampDown: '5s',
        exec: fn,
      },
    };
  }

  return {
    ladder: {
      executor: 'ramping-arrival-rate',
      startRate: LADDER_TARGETS[0],
      timeUnit: '1s',
      // 开环的全部意义：VU 池必须大到不成为新的天花板，否则测的是 k6 自己。
      preAllocatedVUs: Number(__ENV.PRE_VUS || 100),
      maxVUs: Number(__ENV.MAX_VUS || 1000),
      stages: stagesFrom(LADDER_TARGETS),
      exec: fn,
    },
  };
}

function buildThresholds() {
  if (SCENARIO === 'steady') {
    // 真断言。R2 平峰 p95 < 50ms、R3 错误率 < 1%（design § 1.4）。
    // CI 上这些数达不到 —— 所以 CI 用 -e RATE 降速并放宽，见 ci.yml 的注释；
    // 本机跑用默认值。
    return {
      http_req_duration: [`p(95)<${__ENV.P95_MS || 50}`],
      http_req_failed: ['rate<0.01'],
      checks: ['rate>0.99'],
    };
  }
  // 阶梯场景是测量跑，不是门禁 —— 压过拐点本来就该出错，把它判成 fail 没有意义。
  // 唯一有判定意义的 threshold 是运行控制：持续半数请求失败就中止，
  // 不再往一具已经卡死的系统上继续加压（design § 5.1 的五步级联）。
  const th = {
    http_req_failed: [
      { threshold: 'rate<0.5', abortOnFail: true, delayAbortEval: '30s' },
    ],
  };

  // 其余这些是**声明，不是断言**：k6 只有见到某个子指标被声明成 threshold 才会
  // 去计算它。判据写成恒真，目的只是让 k6 按档算出百分位和计数。
  // 这就是 CSV 能被彻底去掉的原因。
  for (let i = 0; i < LADDER_TARGETS.length; i++) {
    th[`http_req_duration{step:${i}}`] = ['p(99)>=0'];
    th[`http_reqs{step:${i}}`] = ['count>=0'];
    for (const cls of ['ok', 'biz409', 'err']) {
      th[`lt_step_status{step:${i},class:${cls}}`] = ['count>=0'];
    }
  }
  return th;
}

export const options = {
  scenarios: buildScenario(),
  thresholds: buildThresholds(),
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  discardResponseBodies: SCENARIO === 'read',
};

// ---------------------------------------------------------------------------
// 场景实现
// ---------------------------------------------------------------------------

function pick(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

// 平峰：读写 20:1（design § 1.3）。创建与取消配对，库存净变化为 0。
export function steadyMix() {
  const r = Math.random();

  if (r < 0.5) {
    const res = http.get(
      `${BASE_URL}/flights?origin=${LOADTEST_ROUTE.origin}&destination=${LOADTEST_ROUTE.destination}`,
      { tags: { endpoint: 'search_flights' } },
    );
    check(res, { 'search 200': (r) => r.status === 200 });
  } else if (r < 0.952) {
    const res = http.get(`${BASE_URL}/flights/${pick(READ_FLIGHTS)}`, {
      tags: { endpoint: 'get_flight' },
    });
    check(res, { 'get_flight 200': (r) => r.status === 200 });
  } else {
    const createRes = createBooking(pick(READ_FLIGHTS));
    if (check(createRes, { 'create 201': (r) => r.status === 201 })) {
      const cancelRes = http.post(
        `${BASE_URL}/bookings/${createRes.json('id')}/cancel`,
        null,
        { tags: { endpoint: 'cancel_booking' } },
      );
      check(cancelRes, { 'cancel 200': (r) => r.status === 200 });
    }
  }
}

// 读路径：只压 getById，打散到 50 个航班。
//
// 不混 search：search 走 Postgres 路由查询返列表，getById 走缓存热点读，成本
// 量级不同，混在一起瓶颈只反映慢的那条（conventions/testing.md § 7）。
// search 由 steady 场景按 20:1 覆盖。
export function spreadRead() {
  const tags = stepTag('get_flight');
  const res = http.get(`${BASE_URL}/flights/${pick(READ_FLIGHTS)}`, { tags });
  recordStatus(tags, res);
  check(res, { 'get_flight 200': (r) => r.status === 200 });
}

// 单航班写：只下单，不取消。
//
// 取消是第二条写路径（design § 5.4），混进来就测不出纯行锁竞争。库存 5,000,000
// 保证整个窗口内不会耗尽 —— 一旦耗尽，测的就是「409 的吞吐量」而不是锁。
export function hotFlightWrite() {
  const tags = stepTag('create_booking');
  const res = createBooking(HOT_FLIGHT, tags);
  recordStatus(tags, res);
  check(res, {
    'create 201': (r) => r.status === 201,
    // 409 = 库存卖光了。写场景里这不该发生 —— 发生了说明 seed 没灌或库存被压干，
    // 该测的东西没测到。它不计入 http_req_failed，所以只有这个 check 会露出来。
    'not sold out': (r) => r.status !== 409,
  });
}

function createBooking(flightId, tags) {
  return http.post(
    `${BASE_URL}/bookings`,
    JSON.stringify({
      user_id: uuidv4(),
      flight_id: flightId,
      passenger_name: 'k6 Load',
      passenger_email: 'k6@example.com',
      seat_count: 1,
    }),
    {
      headers: { 'Content-Type': 'application/json' },
      tags: tags || { endpoint: 'create_booking' },
    },
  );
}

// ---------------------------------------------------------------------------
// 收尾：直接把分档曲线算出来打在屏幕上，并存一份几 KB 的 JSON。
// 不再产 CSV —— 这就是 8.4 GB 变成 10 KB 的地方。
// ---------------------------------------------------------------------------
export function handleSummary(data) {
  const stepSeconds = toMillis(STEP_DURATION) / 1000;
  const axis = MODE === 'recon' ? 'vus' : 'rate';

  const rows = LADDER_TARGETS.map((target, i) => {
    const dur = data.metrics[`http_req_duration{step:${i}}`];
    const reqs = data.metrics[`http_reqs{step:${i}}`];
    const count = (n) =>
      (data.metrics[`lt_step_status{step:${i},class:${n}}`] || { values: {} })
        .values.count || 0;

    const ok = count('ok');
    const biz409 = count('biz409');
    const err = count('err');
    const total = ok + biz409 + err;
    if (!dur || !total) return { target, empty: true };

    const v = dur.values;
    return {
      target,
      empty: false,
      achieved: (reqs ? reqs.values.count : total) / stepSeconds,
      p50: v.med,
      p95: v['p(95)'],
      p99: v['p(99)'],
      max: v.max,
      pct2xx: (100 * ok) / total,
      pct409: (100 * biz409) / total,
      pctErr: (100 * err) / total,
    };
  });

  const report = {
    run_name: RUN_NAME,
    scenario: SCENARIO,
    mode: IS_LADDER_SCENARIO ? MODE : 'constant',
    axis,
    ramp_duration: RAMP_DURATION,
    step_duration: STEP_DURATION,
    dropped_iterations: data.metrics.dropped_iterations
      ? data.metrics.dropped_iterations.values.count
      : 0,
    rows,
  };

  const out = {};
  out[`out/${RUN_NAME}.report.json`] = JSON.stringify(report, null, 2);
  out.stdout = IS_LADDER_SCENARIO
    ? renderLadder(report)
    : textSummary(data, { indent: ' ', enableColors: true });
  return out;
}

function renderLadder(r) {
  const pad = (s, n) => String(s).padStart(n);
  const axisLabel = r.axis === 'vus' ? 'VU' : '目标 req/s';
  let s = `\n=== ${r.run_name} ===\n`;
  s += `场景 ${r.scenario} · 模式 ${r.mode} · 每档 ${r.step_duration}\n\n`;
  s += `${pad(axisLabel, 11)} | ${pad('实际 req/s', 10)} | ${pad('p50', 7)} | ${pad('p95', 7)} | ${pad('p99', 8)} | ${pad('2xx%', 6)} | ${pad('409%', 6)} | ${pad('err%', 6)}\n`;
  s += `${'-'.repeat(84)}\n`;

  let prev = null;
  let knee = null;
  for (const row of r.rows) {
    if (row.empty) {
      s += `${pad(row.target, 11)} |     (无数据 —— 测试在这一档之前就结束了)\n`;
      continue;
    }
    s += `${pad(row.target, 11)} | ${pad(row.achieved.toFixed(1), 10)} | ${pad(row.p50.toFixed(2), 7)} | ${pad(row.p95.toFixed(2), 7)} | ${pad(row.p99.toFixed(2), 8)} | ${pad(row.pct2xx.toFixed(2), 6)} | ${pad(row.pct409.toFixed(2), 6)} | ${pad(row.pctErr.toFixed(2), 6)}\n`;
    if (knee === null) {
      if (r.axis === 'rate' && row.achieved < row.target * 0.95) knee = row;
      else if (r.axis === 'vus' && prev !== null && row.achieved < prev * 1.05) knee = row;
    }
    prev = row.achieved;
  }

  s += '\n';
  if (knee === null) {
    s += r.axis === 'rate'
      ? '⚠ 每一档都跟上了目标速率 —— 拐点在阶梯之外，提高 RATE_MAX 再跑。\n'
      : '⚠ 吞吐一路线性增长，没有平台 —— X_max 在阶梯之外，提高 VU_MAX 再跑。\n';
  } else if (r.axis === 'rate') {
    s += `拐点：目标 ${knee.target} req/s 这一档实际只跑到 ${knee.achieved.toFixed(1)} req/s，p99 = ${knee.p99.toFixed(2)} ms。\n`;
    s += '→ 容量上限在这一档与上一档之间。\n';
  } else {
    s += `吞吐平台 X_max ≈ ${knee.achieved.toFixed(1)} req/s（${knee.target} VU 起不再增长）。\n`;
    s += `→ 开环阶梯用 -e RATE_MAX=${Math.round(knee.achieved)} 跑，自动铺 0.5×–1.5×。\n`;
  }

  if (r.dropped_iterations > 0) {
    s += `\ndropped_iterations = ${Math.round(r.dropped_iterations)} —— k6 自己没能按时发出这么多请求。\n`;
    s += '  数量大时要确认「跟不上目标」是系统的极限还是 k6 的极限（看 MAX_VUS 是否触顶）。\n';
  }

  // 卖光的签名未必是 409：409 洪水会把熔断器打开，之后全是 503（D-01）。
  // 所以判据是「成功占比塌了」，不是「409 出现了」。
  if (r.scenario === 'write') {
    const worst = Math.min(...r.rows.filter((x) => !x.empty).map((x) => x.pct2xx));
    if (isFinite(worst) && worst < 95) {
      s += `\n⚠ 有档的成功下单占比只有 ${worst.toFixed(2)}% —— 这一档没有测到行锁。\n`;
      s += '  409 = 库存被压干，503 = 熔断器打开。先跑 make loadtest-seed 重置库存。\n';
    }
  }
  return s;
}
