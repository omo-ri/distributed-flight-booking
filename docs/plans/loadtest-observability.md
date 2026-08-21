# 压测可观测性收尾计划

> 跨 [T-06](../tasks/T-06-k6-three-scenarios.md)、[D-10](../tasks/D-10-container-resource-limits.md)、[D-11](../tasks/D-11-structured-logging.md) 的一次性执行计划。
> 完成后拆进对应任务条目并归档本文件——拆分建议见文末。

## 为什么

T-06 的 k6 脚本已经能测出东西（写路径拐点已实拍到），但**跑一次的代价高到无法正常工作**：

- 一次 `read` 跑产出 8.4 GB CSV。已在工作区修掉——聚合搬进 k6 内部，改吐几 KB 的 `report.json`
- 两个服务在**正常请求路径**上打日志，违反 [`CLAUDE.md`](../../CLAUDE.md) § 4。`read/recon` 那跑 385 万请求 × 每请求至少 2 行 = 770 万行，几个 G
- `k6/script.js` 437 行里只有约 30 行是"发什么请求"，测试逻辑被 265 行基础设施和 101 行注释埋住

结果是 T-06 有三条验收项**一次都没跑过**（`steady` 场景、`read/ladder`），而已经拿到的 `read` 路径 `X_max ≈ 18894 req/s` 证据已丢失且高度可疑。

**目标**：把"跑一次压测"从昂贵变便宜，然后把 T-06 剩下的跑跑完，拿到可信的容量基线交给 [T-07](../tasks/T-07-local-capacity-baseline.md)。

## 一个必须先说清楚的事实：18894 很可能是假的

`k6/out/read-recon.summary.json`（那跑的残留）显示：385 万请求、全程平均 16105 req/s、**p95 36.8ms / p99 52.8ms / 失败率 0**——这不像一个饱和的系统。更硬的信号是 `http_req_duration.min = -63.6ms`，**负数**，说明那次跑压测机自己的计时已经不正常了。

18894 是当时从 8.4 GB CSV 里分档算出的峰值档，**那份 CSV 已删，表拿不回来**。它比 [`design/system-design.md`](../design/system-design.md) § 1.3 推算的 2000–3000 高一个数量级，这个反差本身就该引起怀疑。

**结论**：`read/recon` 必须重跑，并且要能判断"是系统饱和还是 k6 饱和"。

## 已定的决策

| 议题 | 选择 | 被否掉的与理由 |
|---|---|---|
| 一次跑完手里剩什么 | 分档结论表 + 跑中盯 Grafana；**不留**原始数据（CSV 仅显式 opt-in） | 留原始数据 = 死后尸检，且 98% 是重复标签；缺的不是数据，是跑中能看见系统 |
| 日志止血 | compose 加日志上限 + `LOG_LEVEL` 环境变量，压测设 `warn` | 中间件保留（用户决定）；只加上限不加开关等于让日志有上限但没意义 |
| 脚本长 | 拆职责：`script.js` 只留场景，阶梯计算与报告渲染进 `k6/lib/` | 只砍注释是治标——让人迷路的是 104 行报告渲染代码，它一行注释都不多 |
| 顺序 | 全部改完再跑 | 先跑会写一份注定作废的验证记录（现有记录带行号引用，重构后全失效） |
| `steady` 口径 | 拆掉 create/cancel 配对，恢复"一次迭代 = 一个请求" | 改文档迁就代码会让 `rate` 永远带一个隐式换算系数 |
| 怎么判 k6 饱和 | 主判据看服务端 Grafana，辅以 `docker stats` | 双 k6 进程对照最严谨，但同机压测下对照性打折，且要跑双份时间 |
| `analyze_ladder.py` | 直接删 | 它已跑不起来（缺 `stages.json` 产出源），核心优化是为不再产出的 GB 级 CSV 做的 |
| flight-service 日志 | 止血 + 迁 `slog`/JSON/`LOG_LEVEL` | 不迁会留下半残开关（`LOG_LEVEL` 只对一个服务有效）；trace_id 贯通有真设计不确定性，留给 D-11 |

**分支**：当前在 `docs/design-notes`，工作区已有 15 个改动文件。这一轮跨代码+文档，建议先理清现有改动，另开 `chore/loadtest-observability`。

---

## 阶段一 · 止血（跑之前必须做完）

| # | 动作 | 文件 | 验收 |
|---|---|---|---|
| 1.1 | 13 个服务加 `logging: driver json-file, max-size 50m, max-file 3` | `docker-compose.yml` | `docker inspect` 看到 LogConfig 生效 |
| 1.2 | `LevelInfo` 硬编码 → `envOrDefault("LOG_LEVEL","info")` 解析成 `slog.Level` | `booking-service/cmd/main.go:29` | `LOG_LEVEL=warn` 起服务打一个请求，无 `"request"` 行 |
| 1.3 | 清掉 `k6/out/` 残留 479 MB | — | `du -sh k6/out` 接近 0 |
| 1.4 | 删除 `k6/analyze_ladder.py` 与 `k6/__pycache__/` | — | 文件不存在 |

`envOrDefault` 两个服务都已有（`booking-service/cmd/main.go:152`、`flight-service/cmd/main.go:118`），直接复用。

## 阶段二 · flight-service 迁 slog（D-11 第 1、3 点）

flight-service 用标准库 `log.Printf`，**没有级别概念**，`LOG_LEVEL` 对它完全无效。27 处调用，其中 **10 处在请求路径上**。

| # | 动作 | 文件 |
|---|---|---|
| 2.1 | `main` 里建 `slog.New(slog.NewJSONHandler(os.Stdout, ...))` + `LOG_LEVEL`，与 booking 侧同构；6 处 `log.Fatalf` → `slog.Error` + `os.Exit(1)`（对齐 `booking-service/cmd/main.go:109-110`），5 处启动日志 → `slog.Info` | `flight-service/cmd/main.go` |
| 2.2 | 构造函数注入 logger | `cache.NewRedisCache` / `NewRedisSentinelCache`（`redis.go:23,36`）、`auth.UnaryInterceptor`（`interceptor.go:15`）、`service.NewFlightService`（`flight.go:23`） |
| 2.3 | 按下表处理 10 处请求路径日志 | `redis.go`、`interceptor.go` |

调用点只有 3 处，全在 `flight-service/cmd/main.go:53,59,85`；**无测试引用这些构造函数**，改动低风险。

| 现状 | 处数 | 改成 |
|---|---|---|
| `[CACHE] HIT` / `MISS` | 4 | **删日志**，换 `flight_cache_operations_total{cache,op,result}` counter |
| `[CACHE] SET` | 2 | **删**——成功写缓存没人需要看 |
| `[CACHE] DEL` | 3 | **删** |
| `[AUTH] OK` | 1 | **删**——量等于总请求数 |
| `[CACHE] SET ERR` | 2 | 留，`slog.Warn`（异常但已自动降级） |
| `[AUTH] REJECTED` | 2 | 留，`slog.Warn` |
| `service/flight.go:116` 失效失败 | 1 | 留，`slog.Warn` |

## 阶段三 · 补缓存指标

删掉 HIT/MISS 日志会让缓存变成黑盒——全仓库现在**只有 3 个应用指标**（`flight-service/internal/metrics/metrics.go:19-33`，booking 侧同构），没有任何缓存指标。而"18894 req/s 是不是全部命中缓存"恰恰是这轮压测最该知道的事之一。CLAUDE.md § 4：**没有指标的机制等于不存在。**

| # | 动作 | 文件 |
|---|---|---|
| 3.1 | 加 `flight_cache_operations_total{cache,op,result}` counter，`promauto` 声明方式与现有三个一致 | `flight-service/internal/metrics/metrics.go` |
| 3.2 | 加"缓存命中率"面板（**改文件，不在 UI 点**） | `grafana/dashboards/infrastructure.json` |

标签取值集合有上界：`cache ∈ {flight, search}`、`op ∈ {get, set, del}`、`result ∈ {hit, miss, ok, error}`。

**不改** `redis.go:61-64` 那个"所有 err 都当 MISS"的问题（`redis.Nil` 与连接失败/超时不分）——它是一条独立缺陷，该单独登记，混进日志改造会让这次改动意图变混。`result` 标签给它留了位置，将来补 `error` 取值即可。

## 阶段四 · 拆 k6 脚本（T-06）

437 行 = 101 注释 + 41 空行 + 295 代码。代码里 `handleSummary` + `renderLadder` 占 **104 行**（报告渲染），阶梯脚手架约 60 行，而**真正的测试逻辑只有约 30 行**。

| # | 动作 | 产出 |
|---|---|---|
| 4.1 | 阶梯计算移出：`toMillis` / `currentStep` / `stepTag` / `recordStatus` / `ladderSteps` / `reconSteps` / `stagesFrom` / `defaultRateMax` | `k6/lib/ladder.js` |
| 4.2 | 报告渲染移出：分档表计算 + `renderLadder` | `k6/lib/report.js` |
| 4.3 | `script.js` 瘦到约 80–100 行：常量、`setResponseCallback`、`buildScenario`、`buildThresholds`、三个场景函数、`handleSummary` 薄壳 | `k6/script.js` |
| 4.4 | 长篇论证（闭环 vs 开环、409 为什么排除、CSV 为什么 8.4 GB）搬回 T-06，脚本只留"读代码时必须知道"的 | `docs/tasks/T-06-*.md` |

k6 支持本地相对 import，跑法不变（Makefile 已挂载整个 `k6/` 到 `/scripts`）。

## 阶段五 · 改 `steady` 口径

`constant-arrival-rate` 的 `rate` 计的是**迭代**不是请求，而写支一次迭代发两个请求（create + cancel），所以实际是 **524 req/s、按请求算读写 10:1**，而 `design/system-design.md` § 1.3 写的是 500 QPS / 20:1。

| # | 动作 | 说明 |
|---|---|---|
| 5.1 | `steadyMix` 拆成四支各发一个请求：50% search / 45.24% getById / 2.38% create / 2.38% cancel | `rate=500` 就真是 500 req/s，读写 20:1 |
| 5.2 | cancel 支需要已存在的订单 ID：每个 VU 维护本地数组，create 成功压栈、cancel 弹栈，栈空时退化成 create | 只影响跑的头一两秒 |
| 5.3 | 加按 endpoint 的**恒真** threshold 声明（`http_req_duration{endpoint:search_flights}` 等） | 真断言仍是总体 p95<50ms；分端点数据回答"这 50ms 是被搜索还是 getById 吃掉的" |

库存核对：60s × 500 × 2.38% ≈ 714 单散到 50 个航班（每个 300 座，`k6/loadtest-seed.sql`），够用，但**每跑一次要重灌 seed**（`make loadtest-*` 各目标已依赖 `loadtest-seed`）。

## 阶段六 · 跑（约 25 分钟）

跑中开两个终端：Grafana（<http://localhost:3000>，Services + Infrastructure 两块看板）和 `docker stats`。

| # | 命令 | 时长 | 看什么 |
|---|---|---|---|
| 6.1 | `make loadtest-steady` | 60s | 三条真断言是否通过；**这是从没执行过的新代码** |
| 6.2 | `make loadtest-read-recon` | 5.5min | 新 `X_max`，**与 18894 对照**；判是系统饱和还是 k6 饱和 |
| 6.3 | `make loadtest-read-ladder RATE_MAX=<新 X_max>` | 5.5min | 拐点（实际追不上目标 + p99 起飞） |
| 6.4 | `make loadtest-write-recon` | 5.5min | **回归检查**：与历史值 `X_max ≈ 654` 对照，对不上就是重构错了 |
| 6.5 | `make loadtest-write-ladder RATE_MAX=<新值>` | 5.5min | 拐点（历史：目标 589 时 p50 从 14ms 跳到 846ms） |

**6.2 的判据**：

- 服务端 Grafana 的 RPS 明显低于 k6 报的数 → 中间丢了东西
- 服务端 CPU / PG 连接数远未打满而吞吐已不涨 → 瓶颈不在服务
- `docker stats` 显示 k6 容器吃满核而被测服务没有 → 天花板是 k6
- `dropped_iterations` 与 `MAX_VUS` 是否触顶

## 阶段七 · 记录

| # | 动作 | 文件 |
|---|---|---|
| 7.1 | 更新 T-06 验证记录，**行号全部重取**（重构后旧行号全失效），`status` 视结果改 `done` | `docs/tasks/T-06-*.md` |
| 7.2 | 容量结论写入报告，须记机器配置（12 vCPU / 7.7 GiB，WSL2 + Docker，13 容器同机，压测机同机） | `docs/reports/load/`（**永不修改，只新增**） |
| 7.3 | 日志改动记入 D-11，compose 日志上限记入 D-10 | `docs/tasks/D-11-*.md`、`D-10-*.md` |
| 7.4 | `LOG_LEVEL` 加进环境变量表 | `README.zh-CN.md` § 5、`README.md` 对应处 |

## 可选 · CI 一行修复

`.github/workflows/ci.yml:205` 读 `k6/out/ci-smoke.summary.json`，但 `handleSummary` 只写 `<RUN_NAME>.report.json`——**没有任何地方产出 `.summary.json`**。那个 `if [ -f ]` 永远为假，摘要步骤静默不输出、artifact 上传拿不到东西。

修法二选一：`handleSummary` 对 `steady` 场景额外写一份 `.summary.json`，或把 CI 改成读 `.report.json`。

---

## 验证

- **阶段一/二/三**：`go test -race -count=1 ./...`（两个模块各自）；`docker compose up` 起栈，打一个请求确认 `LOG_LEVEL=warn` 下无正常路径日志、`info` 下有；curl `:9091/metrics` 看到 `flight_cache_operations_total`
- **阶段四/五**：`make loadtest-steady` 能跑通就是拆分没拆坏
- **阶段六**：6.4 的 `write/recon` 与历史 654 对照，这是重构的免费回归检查

**整体判据**：一次 `read` 跑之后，`k6/out/` 只有几 KB、`docker compose logs` 只有几十行，而分档表和 Grafana 曲线都能回答"拐点在哪"。

## 风险与不做什么

**不做**：trace_id 跨 gRPC 贯通（D-11 第 2 点，有真设计不确定性，且压测期间不看日志，本轮零收益）；`redis.go` 的 `err != redis.Nil` 区分（独立缺陷，应单独登记）；CI 门禁调整。

**主要风险**：6.2 如果确认 18894 是 k6 的天花板而不是系统的，`read` 路径的容量结论这一轮拿不到。**那不是失败**——它把一个假数字换成了"这台机器上单进程 k6 测不了读路径"的真结论，但会影响 T-07 排期。到那一步再决定是上多进程 k6 还是承认这个边界。

**次要风险**：5.2 的 cancel 栈在高速率下可能频繁为空（VU 复用率低时），导致实际配比偏向 create。跑完看分端点计数即可确认，偏差大就把 create 比例调高。

---

## 拆分建议

按"什么时候需要改它"和依赖关系，建议拆成四条：

| 拆成 | 内容 | 归属 | 依赖 |
|---|---|---|---|
| **D-11 的一部分** | 阶段一 1.2 + 阶段二（两个服务的 `LOG_LEVEL` 与 flight 迁 slog） | 已登记，补充 booking `requestLogger` 这个实例 | 无 |
| **D-10 的一部分** | 阶段一 1.1（compose 日志上限） | 已登记，把"资源限制"扩到磁盘 | 无 |
| **新 D-xx** | 阶段三（缓存指标）+ `redis.go` 的 `err != redis.Nil` 区分 | 新登记：缓存无指标、缓存故障伪装成未命中 | 阻塞阶段二（删日志前要先有指标） |
| **T-06 剩余** | 阶段一 1.3/1.4 + 阶段四 + 五 + 六 + 七 | 已登记，`status: doing` | 依赖上面三条先完成 |

拆的时候注意两处**真依赖**（不是排序偏好）：

1. **缓存指标必须先于删缓存日志**——否则中间会有一段时间缓存完全不可见
2. **日志止血必须先于阶段六的任何一跑**——`read/ladder` 要压到两万级 req/s，不修就会第二次被淹

阶段四（拆脚本）与阶段五（改口径）可以并行，它们改的是 `k6/` 下不同的东西。
