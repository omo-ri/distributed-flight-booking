# 我们有什么 —— 能力清单

> 盘点截止 2026-08-19。每条能力都指向具体代码位置，可以直接核对。
> 判定标准：**代码里存在、且能跑起来** 才算"有"。写在文档里但没实现的不算。

## 速览

| 领域 | 覆盖度 | 一句话 |
|---|---|---|
| 应用架构 | 🟢 强 | 微服务拆分、职责边界清晰、分层规范 |
| 数据一致性 | 🟡 中 | 单库事务扎实，跨库一致性有已知缺口 |
| 韧性工程 | 🟢 强 | 重试 + 熔断 + 幂等三件套齐全且正确组合 |
| 指标监控 | 🟢 强 | 指标设计考虑了基数，SLO 进了 CI 门禁 |
| 日志 | 🔴 弱 | 两个服务日志格式不一致，无聚合，无关联 |
| 链路追踪 | 🔴 无 | 完全没有 |
| 容器编排 | 🟡 中 | 只有 Compose，无 K8s |
| 发布交付 | 🔴 弱 | CI 完整，CD 为零 |
| 故障演练 | 🟡 弱 | 有演示脚本，但只是 `docker stop` |
| 安全 | 🔴 弱 | 明文密钥、无 TLS |

---

## A. 应用架构

### A-1 微服务拆分与所有权边界 ✅

两个服务各自独占数据，跨服务只能通过 gRPC 接口访问对方的数据。flight-service 不暴露 HTTP 业务接口，无法被外部直接调用。

- `flight-service/cmd/main.go:77` —— 只监听 gRPC 端口
- `flight-service/cmd/main.go:92-99` —— HTTP 端口仅挂载 `/metrics`

**为什么这算能力**：座位库存这种强一致资源有唯一所有权方，并发控制才可能正确。很多"微服务"项目实际上多个服务共享一个库，那不是微服务，只是分布式单体。

### A-2 契约优先的接口定义 ✅

- gRPC 契约：`proto/flight/flight.proto` → `protoc` 生成 `pb/flight/*.go`（`make proto`）
- REST 契约：`booking-service/api/openapi.yaml` → oapi-codegen 生成 `api.gen.go`

接口定义是源，代码是产物。改接口必须改契约文件，编译器会强制所有实现同步更新。

### A-3 分层与依赖方向 ✅

`handler → service → repository → db`，每层单向依赖。

- `booking-service/cmd/main.go:82-84` —— 依赖在 main 里组装注入
- `hw2-marketplace` 更进一步用了 interface + impl 分离，只有 `cmd/main.go` 引用 impl 包

### A-4 错误码到协议状态的映射 ✅

业务错误映射到标准 gRPC code，再映射到 HTTP 状态码：

```
座位不足  → RESOURCE_EXHAUSTED → 409
航班不存在 → NOT_FOUND         → 404
密钥错误  → UNAUTHENTICATED    → (内部)
熔断打开  →                    → 503
```

---

## B. 数据一致性

### B-1 悲观锁保护的库存扣减 ✅

`flight-service/internal/repository/flight.go` —— `SELECT ... FOR UPDATE` 锁住航班行，在同一事务内完成检查、扣减、写预留记录。

**为什么用悲观锁而不是乐观锁**：座位是高竞争的稀缺资源，最后几个座位会有大量并发请求命中同一行。乐观锁在高冲突下会大量重试，反而更慢。

### B-2 幂等的库存操作 ✅

`seat_reservations.booking_id` 唯一约束 + 存在性检查。同一个 `booking_id` 重复 `ReserveSeats` 不会重复扣减。

**这是重试机制的前提条件**。B-2 不存在的话，A 层的重试就是危险的 —— 网络超时后重试会把库存扣两次。

### B-3 数据库层约束兜底 ✅

`flight-service/migrations/001_init.up.sql` —— `CHECK (available_seats >= 0)`、`CHECK (available_seats <= total_seats)`、`CHECK (price > 0)`、航班号+日期唯一索引。

即使应用逻辑写错，数据库也不会接受负库存。

### B-4 价格快照 ✅

`bookings.total_price` 在下单时固化，不是每次读取时重算。航班改价不影响已有订单。

### B-5 自动迁移 ✅

`*/cmd/main.go` 启动时执行 golang-migrate，`ErrNoChange` 被正确忽略。

---

## C. 韧性工程

### C-1 分类重试 ✅

`booking-service/internal/grpcclient/flight.go:23-26`

```go
var retryableCodes = map[codes.Code]bool{
    codes.Unavailable:      true,
    codes.DeadlineExceeded: true,
}
```

只对**可能因重试而改变结果**的错误重试。`NOT_FOUND` 重试一百次结果还是 `NOT_FOUND`，只会浪费下游资源。

指数退避 100 → 200 → 400ms（`flight.go:96`），退避期间监听 `ctx.Done()`，上游取消时立即返回。

### C-2 熔断器三态机 ✅

`booking-service/internal/circuitbreaker/breaker.go`

```
CLOSED ──窗口内失败达阈值──► OPEN ──等待 timeout──► HALF_OPEN
   ▲                                                   │
   └────────────── 探测成功 ────────────────────────────┘
                          探测失败 → 回到 OPEN
```

- 滑动窗口计数（`RecordFailure` 里窗口过期则重置计数）
- HALF_OPEN 只放行探测请求
- 状态迁移打日志（`setState`）
- 参数外部注入，不硬编码

设计上的正确之处：**熔断逻辑封装在独立包，通过泛型函数 `withCircuitBreaker[T]` 包装 gRPC 调用**（`flight.go:59`），不侵入业务代码。有独立单元测试 `breaker_test.go`。

> ⚠️ 失败判定有缺陷，见 [D-01](../tasks/D-01-circuit-breaker-error-classification.md)。

### C-3 Redis Sentinel 接入 ✅

`flight-service/internal/cache/redis.go:23` —— `redis.NewFailoverClient`，客户端向 sentinel 查询 master 地址。master 挂了之后 sentinel 提升 replica，客户端自动跟随，**服务不需要重启**。

### C-4 缓存降级 ✅

`flight-service/cmd/main.go:65-67` —— Redis 地址未配置时服务照常启动，只是无缓存。缓存是可选依赖，不是硬依赖。

### C-5 服务间认证 ✅

`flight-service/internal/auth/interceptor.go` —— unary 拦截器对**所有** gRPC 方法统一校验 `x-api-key`。用拦截器而不是每个 handler 里校验，意味着新增方法不会漏掉鉴权。

---

## D. 可观测性 —— 指标

### D-1 有基数意识的指标设计 ✅

`booking-service/internal/metrics/metrics.go:36-38`

```go
endpoint := c.Path()   // 路由模板 /bookings/:id，不是实际 URL
```

如果用 `c.Request().URL.Path`，每个订单 UUID 都会产生一条新时间序列。这是 Prometheus 被打爆的头号原因。

flight-service 侧用 `info.FullMethod`（`metrics.go:44`），同样是有界集合。

### D-2 跨协议统一的指标命名 ✅

flight-service 是 gRPC，但把指标注册在 `http_*` 名下（`flight-service/internal/metrics/metrics.go:19-21`，注释说明了理由）。代价是命名不精确，收益是**一条 PromQL 覆盖两个服务**，告警规则和看板不用写两遍。

这是一个有意识的权衡，不是疏忽。

### D-3 直方图桶按实际延迟量级选取 ✅

`[0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5]` —— 实测 p95 是 4.86ms，桶从 5ms 起步，密集覆盖了实际分布区间。桶选错的话 `histogram_quantile` 算出来的分位数会严重失真。

### D-4 基础设施指标 ✅

postgres_exporter × 2 + redis_exporter，Grafana Infrastructure 看板覆盖 PG 连接数、commit/rollback 速率、缓冲命中率、Redis ops/内存/客户端数。

**应用指标告诉你"坏了"，基础设施指标告诉你"为什么坏"。** 两者都要有。

### D-5 配置即代码 ✅

Prometheus 抓取配置、告警规则、Grafana 数据源、Grafana 看板 JSON —— 全部在仓库里，容器启动时 provisioning 加载。没有任何"在 UI 上点出来的"配置。

- `prometheus/prometheus.yml`、`prometheus/alerts.yml`
- `grafana/provisioning/`、`grafana/dashboards/`

---

## E. SLO 工程

### E-1 SLI 定义有实测基线支撑 ✅

`metrics-report.json` 记录了真实压测结果（错误率 0%、p95 4.858ms），SLO 阈值（1% / 500ms）相对基线留了余量，失败阈值（5% / 1000ms）再留一层。

**阈值是从数据推出来的，不是拍脑袋定的** —— 这是 SLO 和"随便设个告警"的根本区别。

### E-2 SLO 作为 CI 门禁 ✅

`scripts/verify_metrics.py` —— 压测后查询 Prometheus HTTP API，验证错误率和 p95，违反则 exit 1 使 CI 失败，同时把测量值写入 `metrics-report.json` 作为 CI 产物。

`.github/workflows/ci.yml` 的 `load-test` job 调用它。

**这意味着性能退化能阻断合并**，而不是等上线后被用户发现。

### E-3 告警规则与 SLI 对齐 ✅

`prometheus/alerts.yml` 里每条规则带 `sli:` 标签，表达式与 SLI 定义一致，注释标明了阈值来源。用 `clamp_min(..., 1e-9)` 避免零流量时除零 —— 细节到位。

---

## F. 测试

### F-1 分层测试 ✅

| 层 | 位置 | 数量 |
|---|---|---|
| 单元（带 `-race`） | `*/internal/**/**_test.go` | 熔断器状态机、gRPC handler |
| 集成 | `tests/test_api.py` | 15 |
| E2E（直连双库校验） | `tests/test_e2e_db.py` | 1 |
| 负载 | `k6/script.js` | 三个场景（`steady` / `read` / `write`），阶梯场景带闭环+开环两种模式 |

### F-2 E2E 校验穿透到数据库 ✅

`tests/test_e2e_db.py` —— 不只看 HTTP 响应，而是直连两个 PostgreSQL，验证 `bookings` 行、`seat_reservations` 行、`flights.available_seats` 数值在 create → cancel 全周期的正确性。

**只看 API 响应的 E2E 测不出跨服务状态不一致**。

### F-3 可重复的负载脚本 ✅

`k6/script.js` —— `steady` 场景里创建和取消配对执行，库存净变化为 0；`write` 场景打的是 `k6/loadtest-seed.sql` 灌进去的 500 万座航班，压不干。两条路径都可以反复跑而不耗尽座位，这是负载脚本能进 CI 的前提。

### F-4 开环压测能测出拐点 ✅

`k6/script.js:137` —— `read` / `write` 两个场景用 `ramping-arrival-rate` 按固定速率发压，不管系统回不回得过来。闭环下 `VU数 = 吞吐 × 延迟` 是恒等式，饱和后延迟随 VU 严格线性增长，看不到过载时的非线性恶化；开环才能让请求真的排队。

配套的两件东西：

- `k6/script.js:125` 的 `MODE=recon`（`ramping-vus`）先测出吞吐平台 `X_max`，开环阶梯的范围由它决定而不是由推算决定
- `k6/script.js:165` 的 `abortOnFail`：持续半数请求失败即中止，不再往一具卡死的系统上加压

### F-5 压测口径排除正常业务拒绝 ✅

`k6/script.js:53` —— `setResponseCallback` 把 409 排除出 `http_req_failed`。座位不足是正常业务结果（`design/system-design.md` § 3.3），把它计入错误率就会在压测侧犯下 [D-02](../tasks/D-02-error-rate-sli-server-errors-only.md) 在 SLI 侧犯的同一个错误。

### F-6 阶梯曲线可读 ✅

`k6/analyze_ladder.py` —— k6 收尾的 summary 只给全程一个 p95，那是拐点前后混在一起的数。这个脚本按阶梯档切开 CSV，打印每档的实际速率、p50/p95/p99、2xx/409/其他错误占比与状态码分布，并指出拐点落在哪一档。阶梯定义由 `k6/script.js` 的 `handleSummary` 写进 `k6/out/<run>.stages.json`，不在两处重复。

---

## G. CI

### G-1 四阶段流水线 ✅

`.github/workflows/ci.yml`

```
build ──┐
        ├──► integration ──┐
unit  ──┘                  │
        └──► load-test ────┘
```

### G-2 真实环境集成测试 ✅

CI 里 `docker compose up --build` 起完整 13 容器栈，轮询 `/metrics` 等待就绪（带失败时 dump `docker compose ps` 和日志），**再通过 Prometheus API 确认 target 真的 `up`**，然后才跑 pytest。

不是 mock，不是 testcontainers 起半套 —— 是完整的生产同构环境。

### G-3 失败可诊断 ✅

CI 失败时上传容器日志、k6 摘要、`metrics-report.json` 作为 artifact。

### G-4 路径过滤 ✅

仓库独立后不再需要按路径过滤：push 到 `main` 和任意 PR 都会跑完整 CI。

---

## H. 部分具备 / 薄弱

### H-1 结构化日志 🟡 只有一半

- ✅ booking-service 用 `slog` JSON handler（`cmd/main.go:29`），自定义请求日志中间件记录 method/path/status/latency_ms/request_id
- ❌ flight-service 用标准库 `log`，输出非结构化文本（`[CACHE] HIT flight:xxx`、`[AUTH] OK /flight...`）

两种格式无法用同一套解析规则送进日志系统。

### H-2 请求 ID 🟡 只到边界

booking-service 有 `middleware.RequestID()`，但这个 ID **没有通过 gRPC metadata 传给 flight-service**。跨服务的一次调用无法串联。

### H-3 故障演示 🟡 是演示不是演练

`scripts/demo_alerts.sh` 能触发 `ServiceDown` 和 `HighErrorRate`，有 `status` 子命令查询两边告警状态。但：
- 只有 `docker compose stop` 这一种故障注入手段
- 没有延迟注入、资源耗尽、网络分区
- 没有复盘产物

### H-4 一键操作 🟡 覆盖有限

`Makefile` 有 `proto`/`up`/`down`/`run`/`test`/`stop`，以及压测的 `loadtest-seed` 与四个 `loadtest-*` 目标。缺少 lint、format、生成报告等目标。

---

## 面试时这套东西的说法

按"能讲出多少深度"排序，最能讲的三个：

1. **重试 + 幂等 + 熔断的组合关系**（C-1 / B-2 / C-2）—— 不是三个独立特性，是一条推理链：要重试 → 重试必须幂等 → 幂等靠唯一约束 → 重试放大故障 → 需要熔断兜底 → 熔断参数怎么定。
2. **指标基数控制**（D-1）—— 能说清楚为什么用路由模板、不控制会发生什么、生产上怎么发现基数爆炸。
3. **SLO 进 CI 门禁**（E-1 / E-2）—— 阈值从压测基线推导，违反阻断合并。大部分候选人只能说"配过 Grafana"。

还讲不了的（现在）：K8s、发布回滚、日志排障、故障复盘。这四项恰好是运维岗最核心的，见 [plans/gap-analysis.md](../plans/gap-analysis.md)。
