# 我们要什么 —— 差距分析

本文分两部分：

- **第一部分：已有代码里的真实缺陷**（D-xx）—— 现在就错的东西，修它们本身就是最好的学习材料
- **第二部分：对标目标岗位的能力缺口**（G-xx）—— 还不存在、需要建设的东西

---

# 第一部分：已发现的缺陷

> 这些不是"可以做得更好"，是**当前实现存在的具体问题**。每条都给了定位和触发条件。

## D-01 熔断器把业务错误计入失败统计 🔴 严重

**位置**：`booking-service/internal/grpcclient/flight.go:66-71`

```go
result, err := retry(ctx, method, fn)
if err != nil {
    cb.RecordFailure()      // ← 任何 err 都算失败
} else {
    cb.RecordSuccess()
}
```

`retry()` 对不可重试的错误（`NOT_FOUND`、`RESOURCE_EXHAUSTED`、`INVALID_ARGUMENT`）会立即返回错误。这些错误随后被 `RecordFailure()` 计入熔断统计。

**触发场景**：60 秒内 5 次查询不存在的航班（正常的用户行为，返回 404），熔断器就会打开。此后 30 秒内**所有请求**——包括完全正常的下单——都被拒绝，返回 503。

一个用户的错误输入，能让整个服务对所有人不可用。

**根因**：混淆了两类错误。
- **依赖健康度信号**：`UNAVAILABLE`、`DEADLINE_EXCEEDED`、`INTERNAL` —— 说明下游有问题
- **业务结果**：`NOT_FOUND`、`RESOURCE_EXHAUSTED` —— 下游工作完全正常，只是答案是"没有"

熔断器要保护的是下游过载，只应该对第一类计数。

**修法**：引入 `isCircuitFailure(err) bool`，只对下游健康度相关的 code 计失败。同时给熔断器加 Prometheus 指标（当前状态 gauge + 状态迁移 counter），否则线上熔断打开了没人知道。

---

## D-02 错误率 SLI 包含客户端错误 🔴 严重

**位置**：
- `booking-service/internal/metrics/metrics.go:56-60` —— 4xx 记为 `client_error` 并计入 `http_request_errors_total`
- `flight-service/internal/metrics/metrics.go:49-51` —— 所有非 OK 的 gRPC code 都计入错误
- `prometheus/alerts.yml` —— `HighErrorRate` 对 `http_request_errors_total` 全量求和，不区分 `error_type`

**后果**：可用性 SLI 和 `HighErrorRate`（critical 级）会被客户端行为触发。服务完全健康时，一个写错 URL 的爬虫就能让 SRE 半夜被叫醒。

**这个仓库里已经有证据**：`scripts/demo_alerts.sh` 的 `high-error-rate` 场景，触发手段是**循环请求一个不存在的订单 ID 刷 404**。脚本作者用它来演示告警能响 —— 但它同时精确地演示了这条告警测的不是服务健康度。

**修法**：SLI 的分子只取 `error_type="server_error"`（HTTP 5xx / gRPC `INTERNAL`、`UNAVAILABLE`、`DEADLINE_EXCEEDED`）。4xx 继续采集（它是有用的信号 —— 客户端集成出问题了），但放在单独的告警里，级别降到 warning。

需要同步改：`alerts.yml`、`scripts/verify_metrics.py`、`README.md` 的 SLI 表、Grafana 看板的错误率面板。

---

## D-03 没有优雅停机 🔴 严重（K8s 迁移的硬阻塞）

**位置**：`booking-service/cmd/main.go`、`flight-service/cmd/main.go` —— 全仓库搜不到任何 `signal.Notify` / `Shutdown` / SIGTERM 处理。

进程收到 SIGTERM 直接死掉。正在处理的请求被切断，数据库连接不归还，gRPC 流被中断。

**当前影响有限**（Compose 手动重启偶尔断几个请求），**但迁移到 K8s 后会变成持续性问题**：每次滚动更新、每次扩缩容、每次节点驱逐、每次 HPA 缩容，都会掉一批正在处理的请求。发布期间错误率飙升，而且查不出原因。

**修法**：两个服务都要
1. `signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)`
2. HTTP 侧 `echo.Shutdown(timeoutCtx)`；gRPC 侧 `srv.GracefulStop()`
3. 关闭顺序：先停止接收新请求 → 等待在途请求完成（带超时上限）→ 关闭 DB / Redis 连接
4. K8s 侧配合 `preStop` sleep 和 `terminationGracePeriodSeconds`

**这是阶段 2 的前置条件**，必须先做。

---

## D-04 Redis Sentinel 不构成高可用 🟠 中

**位置**：`docker-compose.yml:58-79`

```
sentinel monitor mymaster redis-master 6379 1
                                            ↑ quorum = 1
```

只部署了 **1 个 sentinel 实例**，quorum 设为 1。

**两个问题**：
1. **Sentinel 自身是单点** —— 它挂了就没有 failover 能力，整个"高可用"归零
2. **quorum=1 无法防脑裂** —— 单个 sentinel 因为网络抖动误判 master 下线，就能单方面发起 failover，可能产生两个 master

**正确形态**：3 个 sentinel，quorum=2。奇数个节点 + 过半数投票，这是所有基于 quorum 的分布式系统（etcd、ZooKeeper、Consul）的通用规律。

**学习价值高**：修完之后可以做一次真实演练 —— 杀掉 master，观察 sentinel 选举日志、failover 耗时、客户端重连时间窗内丢了多少请求。这是很好的复盘素材。

---

## D-05 没有健康检查端点 🟠 中（K8s 迁移的阻塞项）

全仓库没有 `/health`、`/ready`、`/livez`。Compose 的 healthcheck 只覆盖 PostgreSQL 和 Redis，两个 Go 服务没有 healthcheck。

K8s 的 liveness / readiness probe 无处可接。当前只能拿 `/metrics` 凑合，但 `/metrics` 能返回 200 不代表服务能干活 —— 数据库连接断了、下游熔断打开了，`/metrics` 照样 200。

**修法**：区分两类端点，这个区分是面试高频题：

| 端点 | 语义 | 检查内容 | 失败后果 |
|---|---|---|---|
| `/livez` | 进程还活着吗 | 极轻量，只证明事件循环没死锁 | K8s **重启** Pod |
| `/readyz` | 现在能接流量吗 | DB ping、Redis ping、必要下游可达 | K8s **摘除**流量，不重启 |

**最容易犯的错**：把依赖检查放进 liveness。数据库短暂抖动 → liveness 失败 → K8s 重启所有 Pod → 重启后依然连不上 → 无限重启（CrashLoopBackOff），一次小抖动被放大成全站故障。

---

## D-06 跨库一致性缺口：库存泄漏 🟠 中（架构级）

**位置**：`booking-service/internal/service/booking.go` 的创建流程

```
1. ReserveSeats(...)   → 成功，flight-db 扣了座位
2. INSERT bookings     → 失败（booking-db 不可用 / 进程被 kill）
   ⇒ 座位被永久扣除，但没有任何订单记录指向它
```

`seat_reservations` 里留下一条 `ACTIVE` 记录，`available_seats` 少了，没有任何机制会释放它。泄漏是**累积的** —— 每次失败漏一点，最终航班显示满座但实际没人订。

**当前没有任何补偿机制**：没有预留超时、没有对账任务、没有 saga 补偿。

**这是分布式系统的经典问题，有多种解法，各有代价**：

| 方案 | 做法 | 代价 |
|---|---|---|
| 预留超时（推荐先做） | `seat_reservations` 加 `expires_at`，未在 N 分钟内被订单确认则自动 `EXPIRED` 并归还座位 | 需要后台清理任务；需要"确认"这一步 |
| Saga 补偿 | 步骤 3 失败时显式调 `ReleaseReservation` 回滚 | 补偿调用本身也会失败，只是把问题概率降低 |
| 对账任务 | 定时比对两库，找出无主预留 | 有延迟；需要跨库读权限 |
| Outbox 模式 | 订单和事件写在同一事务，异步投递 | 复杂度最高，需要消息队列 |

**学习价值极高**：这是"为什么分布式事务难"的活教材，而且能做出可观测的演示 —— 在步骤 2、3 之间注入故障，看着 `available_seats` 一点点漏掉，然后引入超时机制修复它。

---

## D-07 密钥明文写在配置里 🟠 中

**位置**：`docker-compose.yml:94, 117` —— `AUTH_API_KEY: "super-secret-key"`，还有各处的 `flight_pass` / `booking_pass`，全部明文提交进 Git。

作业环境无所谓，但迁移到 K8s 时必须解决，而且**密钥一旦进过 Git 历史就等于永久泄漏** —— 改了配置文件，历史提交里还在。

**修法路径**：本地 `.env`（已在 `.gitignore`）→ K8s Secret → 加密的 GitOps（SOPS / Sealed Secrets）。

---

## D-08 gRPC 明文传输 🟡 低（学习价值高）

`booking-service/internal/grpcclient/flight.go:39` —— `grpc.WithTransportCredentials(insecure.NewCredentials())`

API Key 以明文在网络上传输，任何能抓包的人都能拿到它然后直接调 flight-service。

**修法**：mTLS。学习价值在于会完整走一遍证书链 —— 自签 CA、签发服务端/客户端证书、SAN 配置、证书轮换。JD 里的"TCP/IP、网络协议分析能力"，TLS 握手是最好的抓包练习对象。

---

## D-09 监控数据不持久 🟡 低

`docker-compose.yml:163` —— `--storage.tsdb.retention.time=1h`，且 Prometheus 和 Grafana **都没有 volume**。

容器一重启，所有历史指标丢失。1 小时保留期意味着无法做任何跨天的趋势分析、容量规划、SLO 周期统计（error budget 通常按 28 天算）。

**修法**：加 volume；保留期至少 15 天；后续考虑 remote write 到长期存储。

---

## D-10 容器没有资源限制 🟡 低

`docker-compose.yml` 里 **0 处** `mem_limit` / `cpus` / `deploy.resources`，也 **0 处** `restart:` 策略。

任何一个容器内存泄漏都能拖垮整台宿主机。K8s 迁移时这会变成必须回答的问题：requests 和 limits 分别设多少？

**这个知识点面试必考**：
- `requests` 决定调度（能不能放进这个节点）和 QoS 分级
- `limits` 决定运行时上限（CPU 被 throttle，内存超了直接 OOMKill）
- 三种 QoS：Guaranteed（requests == limits）/ Burstable / BestEffort，节点内存压力时按倒序驱逐

**怎么定值**：不能拍脑袋，要从压测数据推。这正好接上已有的 k6 基线。

---

## D-11 日志不可用于排障 🟠 中

三个独立问题：

1. **格式不一致**：booking-service 是 slog JSON，flight-service 是标准库文本（`[CACHE] HIT flight:xxx`）。一套解析规则覆盖不了两个服务。
2. **无关联**：booking-service 有 request_id，但没有通过 gRPC metadata 传给 flight-service。一次跨服务调用在两边的日志里无法串联。
3. **噪音**：`flight-service/internal/auth/interceptor.go:38` 每个请求成功都打一行 `[AUTH] OK`；cache 每次 HIT/MISS/SET/DEL 都打日志。这些在压测量级下会淹没真正有用的信息，也是可观测的性能开销。

**结果**：出了故障只能靠 `docker compose logs` 肉眼翻，这不是运维手段。

---

## D-12 Alertmanager 没有接收端 🟡 低

`alertmanager/alertmanager.yml` —— receiver 是空的，注释里承认了。告警只在 Alertmanager UI 里显示，不会触达任何人。

作业演示够用，但"告警"的定义就是**主动触达**。没有触达渠道，等于没有告警。

---

## D-13 有 SLO 但没有 error budget 🟠 中（方法论缺口）

当前告警是**静态阈值 + 固定持续时间**（错误率 >5% 持续 2m）。这套做法有两个问题：

- **烧得慢的故障漏报**：错误率稳定在 4.9%，永远不触发告警，但一个月下来早就把可用性预算烧光了
- **短促尖峰误报**：一次部署导致 10 秒内错误率 30%，2 分钟窗口可能触发，但实际影响微不足道

**正确做法**：多窗口多燃烧率告警（Google SRE Workbook 第 5 章）

| 窗口组合 | 燃烧率 | 含义 | 响应 |
|---|---|---|---|
| 1h + 5m | 14.4× | 2 天烧完 30 天预算 | 立即呼叫 |
| 6h + 30m | 6× | 5 天烧完 | 呼叫 |
| 3d + 6h | 1× | 正好烧完 | 工单 |

短窗口用来快速恢复（故障停止后告警及时消音），长窗口用来抑制误报。

这是 SRE 方法论中最能体现"懂不懂"的部分，也是面试区分度最高的话题之一。

---

## D-14 告警没有 runbook 🟠 中

三条告警规则的 annotations 只有 summary 和 description，**没有 `runbook_url`**。

告警响了，值班的人打开一看"HighErrorRate on booking-service"，然后呢？查什么？看哪个看板？常见原因是什么？怎么止血？

**运维岗对这个的重视程度远超开发岗**。每条告警必须对应一份 runbook：影响面 → 快速诊断步骤 → 常见原因 → 止血手段 → 升级路径。

---

## D-15 容器以 root 运行 🟡 低

两个 Dockerfile 的运行阶段都没有 `USER`，容器内进程是 root。也没有 `.dockerignore`（构建上下文把 `.git`、测试文件全塞进去了）。

K8s 侧对应 `securityContext`：`runAsNonRoot: true`、`readOnlyRootFilesystem: true`、`allowPrivilegeEscalation: false`、`capabilities.drop: [ALL]`。

---

## D-16 压测无法回答容量问题 🟡 低

`k6/script.js` 固定 10 VU / 30s。它能回答"有没有退化"，回答不了：

- 系统的吞吐拐点在哪？
- 什么资源先成为瓶颈（CPU / PG 连接池 / Redis）？
- 需要几个副本才能撑住 N QPS？

**缺阶梯压测**（ramping-arrival-rate，逐级加压直到 SLO 破线）。没有这个数据，D-10 的资源限制和后续的 HPA 阈值都只能拍脑袋。

---

## D-17 构建产物被提交进 Git 🔵 轻微

`git ls-files` 显示 `metrics-report.json` 和 `k6/out/summary.json` 是被跟踪的，但 `.gitignore` 里写了 `k6/out/`（对已跟踪文件无效）。

这两个是运行时产物，应该只作为 CI artifact 存在。

---

## 缺陷优先级汇总

| ID | 缺陷 | 级别 | 阻塞什么 |
|---|---|---|---|
| D-03 | 无优雅停机 | 🔴 | **阶段 2 K8s 迁移** |
| D-05 | 无健康检查端点 | 🟠 | **阶段 2 K8s 迁移** |
| D-01 | 熔断器统计业务错误 | 🔴 | 熔断可信度 |
| D-02 | 错误率 SLI 含客户端错误 | 🔴 | **整个 SLO 体系的正确性** |
| D-11 | 日志不可排障 | 🟠 | **阶段 5 故障演练** |
| D-13 | 无 error budget | 🟠 | SRE 方法论 |
| D-06 | 库存泄漏 | 🟠 | 数据正确性 |
| D-04 | Sentinel 单点 | 🟠 | HA 有效性 |
| D-14 | 无 runbook | 🟠 | 可运维性 |
| D-07 | 明文密钥 | 🟠 | 阶段 2 |
| D-09 D-10 D-12 D-15 D-16 | 持久化 / 资源 / 通知 / 安全 / 容量 | 🟡 | 各自阶段 |
| D-08 D-17 | mTLS / 产物入库 | 🔵 | — |

---

# 第二部分：对标岗位的能力缺口

目标岗位：**米哈游 平台研发-游戏运维工程师**（2027 届校招）

## JD 逐条对照

| # | JD 要求 | 现状 | 缺口 |
|---|---|---|---|
| 1 | 服务器及基础设施的部署、发布、监控、巡检、日常运维 | 🟡 监控有，部署仅 Compose，无巡检 | G-01 G-04 |
| 2 | 上线、版本发布、扩缩容、故障处理 | 🔴 无 CD、无扩缩容、无故障预案 | G-02 G-03 G-05 |
| 3 | 云原生平台建设，K8s / Docker 容器化 | 🔴 K8s 零覆盖 | **G-01** |
| 4 | CI/CD、自动化运维平台，脚本开发 | 🟡 CI 完整，CD 为零 | G-02 G-06 |
| 5 | Prometheus、Grafana、**ELK/Loki** | 🟡 指标强，日志为零 | **G-04** |
| 6 | 故障响应、问题复盘、稳定性优化 | 🔴 只有演示脚本 | **G-05** |
| 7 | 网络、存储、数据库、中间件运维优化 | 🟡 用了但没运维过 | G-07 |
| 要求 3 | Linux、shell 脚本、排查命令 | 🟡 有 bash 脚本，无系统排查实践 | G-07 |
| 要求 4 | TCP/IP、HTTP、DNS 分析能力 | 🔴 项目里零体现 | G-07 |
| 加分 1 | 阿里云 / 腾讯云 / AWS | 🔴 全本地 | G-08 |
| 加分 2 | Linux、Go、K8s、云计算项目经验 | 🟡 Go 有，K8s 无 | G-01 |

## 能力缺口清单

### G-01 Kubernetes 编排 🔴 最大缺口

JD 第 3 条整条 + 加分项。当前完全空白。

需要覆盖：Deployment / StatefulSet / Service / Ingress、liveness 与 readiness 的区别、requests/limits 与 QoS、HPA、PDB、ConfigMap / Secret、亲和性与拓扑分布、Helm 或 Kustomize 多环境参数化。

数据库用 StatefulSet + PVC，会踩到有状态服务的真实问题（有序启停、稳定网络标识、存储生命周期）。

**前置**：D-03（优雅停机）、D-05（健康检查）必须先修。

### G-02 持续交付 🔴

JD 第 2、4 条。当前 CI 到测试为止，**没有任何东西被部署过**。

需要覆盖：镜像构建与语义化 tag、推送到 registry（GHCR）、GitOps（Argo CD）、渐进式发布（金丝雀 / 蓝绿）、基于 SLI 的自动回滚、发布过程的可观测性。

**这条对应 JD 里"版本发布"—— 游戏行业发版频率高、影响面大，这是核心工作内容。**

### G-03 弹性伸缩 🟠

JD 第 2 条明确提"扩缩容"。需要 HPA（基于 CPU 和自定义指标）、扩容时的冷启动问题、缩容时的优雅停机（依赖 D-03）、以及**为什么有状态服务不能简单水平扩展**。

### G-04 日志体系 🔴

JD 第 5 条明确点名 ELK/Loki。当前为零（D-11）。

需要覆盖：两服务统一结构化日志、trace_id 跨 gRPC 传播、Loki + Promtail/Alloy 采集、LogQL 查询、Grafana 里指标与日志联查（从异常曲线一键跳到对应时间窗的日志）。

配套上 OpenTelemetry 链路追踪 —— booking → gRPC → flight 这条链路正好是教科书级的示例。

### G-05 故障演练与复盘 🔴 面试价值最高

JD 第 6 条。当前只有 `docker stop`（D-03 演示脚本）。

需要建一个**故障场景库**，每个场景包含：注入手段、预期现象、实际观测、恢复过程、MTTR、复盘文档。

候选场景（大部分能直接验证前面的缺陷）：

| 场景 | 注入方式 | 验证什么 |
|---|---|---|
| Redis master 宕机 | kill 容器 | Sentinel failover 耗时、期间丢多少请求（也暴露 D-04） |
| 下游高延迟 | toxiproxy 注入 500ms | 熔断器状态迁移、上游超时传播 |
| PG 连接池耗尽 | 打满连接 | 连接池配置、排队 vs 失败 |
| Pod OOMKilled | 限制内存后压测 | K8s 重启行为、QoS 驱逐顺序（依赖 G-01） |
| DNS 解析失败 | 改 CoreDNS | K8s 内服务发现机制（依赖 G-01） |
| 磁盘写满 | 填充卷 | PG 只读降级行为 |
| 发布过程中断 | 滚动更新中途取消 | 新旧版本共存、优雅停机（依赖 D-03、G-02） |

**每个场景写一份完整 postmortem**：时间线、影响面（多少请求受影响、多长时间）、检测手段（哪条告警响了 / 为什么没响）、根因、行动项、以及**改进后重新演练的对比数据**。

面试时大部分候选人只能说"我配过 Prometheus"。能说"我复现过 Redis failover，MTTR 是 8 秒，期间丢了 43 个请求，加了客户端重试后降到 0"，这是完全不同的量级。

### G-06 运维自动化 🟠

JD 第 4 条"通过脚本开发和工具建设减少重复劳动"。

需要：巡检脚本（定时检查证书有效期、磁盘、副本状态、备份完整性并出报告）、告警自愈（特定告警触发自动处置）、一键诊断工具（收集某次故障时间窗内的全部指标/日志/事件）。

**这是"vibe coding 自动化开发"最合适的落点** —— 但必须在有了 G-04、G-05 之后。自动化一团乱麻只会得到自动化的乱麻。

### G-07 Linux / 网络 / 中间件基础 🟠

JD 任职要求 3、4 和职责第 7 条。当前项目"用了"这些东西但从没"运维过"。

需要变成可展示的实践：
- **网络**：tcpdump 抓 gRPC 握手与 HTTP/2 帧、抓 TLS 握手过程、`ss` 看连接状态与 TIME_WAIT、K8s 内 DNS 解析路径、MTU 与分片
- **Linux 排查**：从"服务慢"出发的完整路径 —— `top`/`vmstat`/`iostat` → `pidstat` → `strace`/`perf` → 火焰图
- **PostgreSQL**：慢查询日志、`EXPLAIN ANALYZE`、索引设计、连接池参数、vacuum 与膨胀、主从复制
- **Redis**：内存淘汰策略、大 key 与热 key、持久化 RDB/AOF 的取舍、`SLOWLOG`

### G-08 公有云 🔵 加分项

加分项 1。优先级最低，但可以在阶段 3 之后把集群搬到一个云托管 K8s（阿里云 ACK 有学生额度），顺带覆盖云上的负载均衡、对象存储、托管数据库。

## 游戏运维的行业特殊性

米哈游是游戏公司，游戏运维和通用互联网运维有几个明显差异，项目往这个方向靠会更贴岗位：

| 游戏运维特点 | 可以在本项目里怎么体现 |
|---|---|
| **区服模型** —— 同一套代码部署 N 份互相隔离的实例 | 用 Kustomize overlay 部署多个 zone，各自独立数据库，共享监控面 |
| **开服/合服/停服维护** | 维护模式开关（返回统一维护公告）、有计划停机流程、数据迁移演练 |
| **版本发布节奏密集，回滚要求极高** | 金丝雀 + 自动回滚（G-02） |
| **流量极端不均** —— 版本更新瞬间涌入 | 阶梯压测找拐点（D-16）、预扩容策略、限流 |
| **长连接 / 有状态会话** | 当前是无状态 REST，可以补一个 WebSocket 侧车服务体验有状态服务的运维难点 |

这不是必须做的，但在面试里能体现"我了解这个行业在运维什么"。

---

## 下一步

见 [05-roadmap.md](./05-roadmap.md)。

一句话建议：**从阶段 1（可观测性补全）开始**。它同时修掉 D-11、为 G-04 打底，并且是阶段 5 故障演练的前提 —— 没有能查的日志和链路，演练出故障你只能干瞪眼。
