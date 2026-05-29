# 第7次课后作业 — CI/CD, Testing & Observability

在第 3 次作业（gRPC + Redis 的航班预订系统）基础上，本次新增 CI 流水线、自动化测试、Prometheus 指标、Grafana 仪表盘、k6 负载测试。

> 第 3 次作业的题目要求与系统设计见本文后半部分，未做改动。

---

## 系统能干嘛

两个微服务组成的航班预订系统：

```
浏览器/客户端 ──HTTP──▶  booking-service (:8080)  ──gRPC──▶  flight-service (:50051)
                              │                                    │
                          booking-db (5434)                   flight-db (5433)
                                                                   │
                                                            Redis Sentinel
                                                       (master / slave / sentinel)
```

booking-service 暴露的 REST API（详细 schema 见 `USAGE.md`）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/flights?origin=&destination=&date=` | 按路线搜航班 |
| GET | `/flights/{id}` | 单个航班详情 |
| POST | `/bookings` | 创建预订（跨库扣座） |
| GET | `/bookings/{id}` | 单个预订 |
| GET | `/bookings?user_id=` | 用户预订列表 |
| POST | `/bookings/{id}/cancel` | 取消预订（归还座位） |
| GET | `/metrics` | Prometheus 指标（本次新增） |

flight-service 是纯 gRPC，对外仅暴露 `/metrics` (`:9091`)。

数据库启动时自动加载 3 个种子航班：

| flight_number | 路线 | 座位 | 票价 |
|---|---|---|---|
| SU1234 | SVO→LED | 180 | 15000 |
| SU5678 | SVO→LED | 120 | 12000 |
| DP402  | VKO→LED | 189 | 8000  |

---

## 一键启动

```bash
docker compose up --build -d           # 启 12 个容器
pip install -r tests/requirements.txt  # 第一次需要
pytest tests/ -v                       # 16 个测试
docker compose down -v                 # 收摊
```

启完后这些端口对外开：

| 端口 | 服务 |
|---|---|
| 8080 | booking-service REST + `/metrics` |
| 50051 | flight-service gRPC |
| 9091 | flight-service `/metrics` |
| 9090 | Prometheus |
| 3000 | Grafana（匿名 Viewer，免登录直接看；admin/admin 可编辑） |
| 5433 / 5434 | flight-db / booking-db |

---

## 本次作业 10 分对照

| 题号 | 分数 | 实现 |
|---|---|---|
| **1** CI pipeline | 1 | `.github/workflows/hw3-ci.yml`，4 job：`build` / `unit` / `integration` / `load-test`；按路径过滤到 `hw3/**`，push 和 PR 都触发 |
| **2** 集成测试 | 1 | `tests/test_api.py`，15 个用例，跨 booking→gRPC→flight→两库 |
| **3** E2E 测试 | 1 | `tests/test_e2e_db.py`，**直连两个 PG 校验** booking 行 + seat_reservation 行 + available_seats 数值（create → cancel 全流程） |
| **4** Prometheus + 指标 | 1 | 两服务各自暴露 `http_requests_total` / `http_request_errors_total` / `http_request_duration_seconds`；Prometheus 容器 5s scrape，6 个 target |
| **5** Grafana 服务仪表盘 | 1 | `grafana/dashboards/services.json`，4 panel：throughput / p50-p95-p99 / error rate / status 分布；provisioning 自动加载 |
| **6** Grafana 基础设施仪表盘 | 1 | `grafana/dashboards/infrastructure.json`，7 panel：postgres 连接 / 事务速率 / 缓存命中率 / Redis ops/内存/客户端；用 postgres_exporter ×2 + redis_exporter |
| **7** 负载测试入 CI | 1 | `k6/script.js`，10 VU × 30s，thresholds `p95<500ms` + `error<1%`；CI 跑 k6 容器，summary 上传 artifact |

> 题 8–10（CI 中 PromQL 阈值校验 / Alert rules / SLI-SLO）暂未做。

---

## 怎么验

### 1. 看本地栈状态

```bash
docker compose ps                 # 12 个容器全 running/healthy
curl :8080/metrics | head -5      # booking-service 指标
curl :9091/metrics | head -5      # flight-service 指标
curl :9090/api/v1/targets | python3 -m json.tool | grep -E '"health"|"job"'
# 预期：6 个 target 全 "up"（booking/flight/prometheus + postgres-booking/postgres-flight/redis）
```

### 2. 跑测试

```bash
pytest tests/ -v
# 预期：16 passed
```

### 3. 跑负载

```bash
docker run --rm --network host -v "$PWD/k6:/scripts" -w /scripts \
  -e BASE_URL=http://localhost:8080 grafana/k6:0.55.0 run script.js
# 预期：p95 远低于 500ms，error rate 0%，全部 ✓
```

### 4. 看 CI

GitHub 仓库 → Actions 标签 → 最新 run。`integration` job 的输出里有：
- `booking-service ready after X attempts`
- `targets: {'booking-service': 'up', 'flight-service': 'up', 'prometheus': 'up'}`
- 末尾 `16 passed`

`load-test` job 的输出里有 `p95 duration_ms` / `error rate` 等汇总，artifact 区可下载 `k6-summary.json`。

---

## Grafana 走查

打开 <http://localhost:3000>，左侧 Dashboards → **hw3** 文件夹下两个面板：

### `hw3 / Services (booking + flight)` — 对应题 5

| Panel | 看什么 | PromQL |
|---|---|---|
| Throughput (RPS) by service | 每秒请求数，两服务分线 | `sum by (service) (rate(http_requests_total[1m]))` |
| Latency percentiles | p50/p95/p99 三档延迟 × 服务 | `histogram_quantile(0.95, sum by (le,service) (rate(http_request_duration_seconds_bucket[1m])))` |
| Error rate (%) by service | 错误请求占比 | `sum by (service) (rate(http_request_errors_total[1m])) / clamp_min(sum by (service) (rate(http_requests_total[1m])), 1e-9)` |
| Requests by status | 按状态码堆叠 | `sum by (status) (rate(http_requests_total[1m]))` |

### `hw3 / Infrastructure (PostgreSQL + Redis)` — 对应题 6

| Panel | 看什么 |
|---|---|
| Exporter health | 三个 exporter 是否存活，DOWN 时变红 |
| PG Active connections by db | 两个库各自的活跃连接数 |
| PG Transaction rate | commit / rollback 速率（rollback 飙升 = 业务在抛错） |
| PG Cache hit ratio | 缓冲池命中率（健康值接近 1） |
| Redis Ops/sec | Redis 命令处理速率 |
| Redis Memory used | 内存使用 + 上限 |
| Redis Connected clients | 活跃客户端数 |

**让仪表盘动起来**：另开一个终端跑一轮 k6（见上一节），右上角时间窗调到 Last 5 minutes，曲线 5s 一刷自动出现。

---

## 答辩可能被问的话术

| 问 | 答 |
|---|---|
| 你 CI 跑了什么？ | 4 个 job。build 和 unit 并行验 Go 编译 + 单测；integration 起完整栈、用 `/metrics` 做就绪探针、查 Prometheus API 断言 target 真的 `up`、跑 16 个 pytest（含 1 个直连两库的 E2E）。load-test 起栈跑 k6，thresholds 违反就 exit 1。|
| 为什么 flight-service 的 `/metrics` 在另一个端口？ | gRPC 和 HTTP 不能共用 listener，所以单开一个 `:9091` 的 HTTP server 暴露 promhttp handler。|
| 这三个指标 label 怎么选的？ | endpoint 用 Echo 的 route pattern（`/bookings/:id`）而非真实 URL，避免高基数。gRPC 端用 `info.FullMethod`。error_type 在 HTTP 侧分 client_error / server_error，gRPC 端用 gRPC code。|
| p95 延迟怎么算？ | `histogram_quantile(0.95, ...)` 作用在 histogram 桶的 5s 累积速率上，所以是滚动 1m 窗口的 p95。|
| 缓存命中率为啥用 rate 而不是 counter 直接除？ | counter 是从启动到现在的累积，瞬时除会被历史平均；rate 是过去 5min 的，能反映当前状态。|
| k6 为啥 create+cancel 配对？ | 否则 30s 内会持续吃座位，跑几次就 RESOURCE_EXHAUSTED；配对让总库存中性，测试可重复跑。|

---



## 🎯 目标

设计并实现一个由两个微服务组成的分布式机票预订系统，两个服务通过 gRPC 通信，并使用 Redis 进行缓存。

实现语言和框架**不限**。

---

## 📊 评分规则

| 模块 | 分值 | 条件 |
|------|------|------|
| 基础架构与集成 | 1–4 分 | 必做 |
| 事务、认证、缓存 | 5–7 分 | 须完整完成 1–4 分模块 |
| 高可用性 | 8–10 分 | 须完整完成 5–7 分模块 |

> ⚠️ 各模块**按顺序评分**：只有前一模块**全部完成**，才会评阅下一模块。

---

## 🏗️ 系统架构

系统由两个微服务组成，各自拥有独立数据库。

```
客户端 (REST) → Booking Service → (gRPC) → Flight Service
                      ↓                          ↓
                 PostgreSQL               PostgreSQL + Redis
```

### Booking Service（预订服务）
- 管理机票预订
- 面向客户端提供 REST API
- 将预订数据存储于独立的 PostgreSQL

### Flight Service（航班服务）
- 管理航班与座位
- 面向内部通信提供 gRPC API
- 将航班数据存储于 PostgreSQL
- 完成第 7 题及以上时，使用 Redis 进行缓存

> 📌 每个服务拥有**各自独立**的数据库。

---

## ⚙️ 通用要求

- 所有服务通过一条命令 `docker-compose up` 启动
- 数据库迁移在**启动时自动执行**
- `.proto` 文件和接口契约由**学生自行设计**

> 📝 本作业**有意不提供**完整的接口规范、请求/响应格式及数据库表结构。附录中仅描述各方法的**行为**——即系统应该做什么。`.proto` 设计、REST 契约、表结构如何实现——**由学生自己决定**，这也是作业的一部分。

---

## 📦 模块一：基础架构（1–4 分）

### 第 1 题：Flight Service 的 gRPC 契约 _(1 分)_

需自行设计 Flight Service 的 `.proto` 文件。

#### 必须实现的方法

| 方法 | 描述 |
|------|------|
| `SearchFlights` | 按路线和日期搜索航班 |
| `GetFlight` | 按 ID 获取航班信息 |
| `ReserveSeats` | 为预订锁定座位 |
| `ReleaseReservation` | 取消预订（释放座位） |

#### 要求

- 核心方法须体现业务操作（锁座、搜索），而非仅是对数据库表的简单封装
- 使用 `protoc` 从 `.proto` 文件**代码生成**
- 日期使用 `Timestamp`，状态使用 `enum`
- 为业务错误定义标准 gRPC 错误码（`NOT_FOUND`、`RESOURCE_EXHAUSTED` 等）

---

### 第 2 题：符合 3NF 的 ER 图 _(1 分)_

需自行设计两个服务的数据库结构。

#### 核心实体

**`Flight`**（Flight Service）— 航班
- 航空公司、路线（机场使用 IATA 代码，如 `VKO`、`LED`）
- 起飞/到达时间
- 座位数（总数与可用数）、票价、航班状态
- 状态值：`SCHEDULED`、`DEPARTED`、`CANCELLED`、`COMPLETED`
- **航班号 + 出发日期**的组合唯一（例如 `SU1234` 于 `2026-04-01`）

**`SeatReservation`**（Flight Service）— 座位预留
- 关联到具体航班及 Booking Service 中的预订记录
- 存储已预留座位数和预留状态
- 状态值：`ACTIVE`、`RELEASED`、`EXPIRED`
- 一条预订记录对应**恰好一条**座位预留

**`Booking`**（Booking Service）— 预订记录
- 乘客信息、关联 Flight Service 的航班 ID、座位数、总价
- 状态值：`CONFIRMED`、`CANCELLED`

#### ER 图要求

- 格式：`dbdiagram.io`、`draw.io`、`PlantUML`、`Mermaid` 任选
- 满足**第三范式（3NF）**
- 须有数据完整性约束：
    - 可用座位数**不能为负**
    - 总座位数和票价必须**严格大于零**

---

### 第 3 题：PostgreSQL + 两个服务的实现 _(1 分)_

- 接入 **2 个独立的 PostgreSQL**（每个服务各一个）
- 使用 `Flyway`、`Liquibase` 或其他迁移工具
- **Booking Service** 实现 REST API 接口
- **Flight Service** 实现 gRPC 服务端，包含第 1 题中的所有方法

> 方法详细说明见附录一。

---

### 第 4 题：通过 gRPC 实现服务间通信 _(1 分)_

Booking Service 在创建和取消预订时，通过 gRPC 调用 Flight Service。

#### 创建预订的必要流程

```
1. Booking Service → GetFlight        — 获取航班信息（含票价）
2. Booking Service → ReserveSeats     — 原子性地锁定座位
3. 锁定成功                           — 快照价格并写入数据库
4. 锁定失败                           — 不创建预订，向客户端返回错误
```

---

## 🔐 模块二：事务、认证、缓存（5–7 分）

### 第 5 题：事务一致性 _(1 分)_

**Flight Service 中：**
- 锁座：`available_seats` 减少 + 创建 `SeatReservation` 须在**同一事务**中完成
- 使用 `SELECT FOR UPDATE` 防止最后一个座位的并发预订（竞态条件）
- 取消锁座：归还 `available_seats` + 更新预留状态须在**同一事务**中完成

**Booking Service 中：**
- 若 gRPC 调用 `ReserveSeats` 失败，则预订记录**不得创建**
- 不允许出现部分提交的状态

---

### 第 6 题：服务间调用的认证 _(1 分)_

为服务间 gRPC 调用实现认证，选择以下**一种**方案：

| 方案 | 说明 |
|------|------|
| **API Key** | 在 gRPC metadata 中传递密钥 |
| **Service JWT** | 在 metadata 中传递服务 Token |
| **Basic Auth** | 在 metadata 中传递用户名/密码 |

#### 要求

- Flight Service 对**所有方法**进行认证校验
- 缺少凭据或凭据无效时，返回 gRPC 错误 `UNAUTHENTICATED`
- 凭据通过容器的**环境变量**传入

---

### 第 7 题：Redis 缓存 _(1 分)_

在 Flight Service 中使用 Redis 进行缓存。

#### 缓存内容

| 数据 | Key 示例 | TTL |
|------|----------|-----|
| 航班信息 | `flight:{id}` | 5–10 分钟 |
| 搜索结果 | `search:{origin}:{destination}:{date}` | 5–10 分钟 |

#### 要求

- Redis 在 `docker-compose` 中启动
- 实现 **Cache-Aside** 策略：查缓存 → 未命中时查数据库 → 写入缓存并设置 TTL
- 所有 Key 必须设置 **TTL**（禁止永不过期的缓存）
- 数据变更时（`UpdateFlight`、`ReserveSeats`、`ReleaseReservation`）须**主动失效缓存**
- 须有 cache **hit/miss 日志**

---

## 🛡️ 模块三：高可用性（8–10 分）

### 第 8 题：调用 Flight Service 的重试机制 _(1 分)_

Booking Service 对 Flight Service 的 gRPC 调用实现重试。

#### 要求

- 最多重试 **3 次**
- 使用**指数退避**（例如：100ms → 200ms → 400ms）
- **仅对以下错误重试**：`UNAVAILABLE`、`DEADLINE_EXCEEDED`
- **不重试**：`INVALID_ARGUMENT`、`NOT_FOUND`、`RESOURCE_EXHAUSTED`
- 对变更操作（`ReserveSeats`）保证**幂等性**——使用相同 `booking_id` 重复调用不会产生重复预留

---

### 第 9 题：Redis 集群模式 _(1 分)_

在 `docker-compose` 中以高可用配置运行 Redis：

| 模式 | 配置要求 |
|------|----------|
| **Sentinel** | 1 主节点 + 1 从节点 + 1 Sentinel |
| **Cluster** | 至少 3 个主节点 |

#### 要求

- 应用代码使用支持 **Sentinel/Cluster** 的客户端

---

### 第 10 题：熔断器（Circuit Breaker） _(1 分)_

Booking Service 对 Flight Service 的调用实现**熔断器**模式。

#### 状态机

```
CLOSED ──(错误累积到阈值)──► OPEN ──(超时后)──► HALF_OPEN
  ▲                                                   │
  └─────────────(探测请求成功)──────────────────────┘
                                    │
                            (探测请求失败)
                                    │
                                    ▼
                                  OPEN
```

| 状态 | 行为 |
|------|------|
| **CLOSED（关闭）** | 正常工作，请求发往 Flight Service。错误累积达到阈值后 → 转为 OPEN |
| **OPEN（断开）** | 所有请求**立即失败**，不发起真实调用。超时后 → 转为 HALF_OPEN |
| **HALF_OPEN（半开）** | 放行一个探测请求。成功 → CLOSED，失败 → OPEN |

#### 要求

- 以 **interceptor/middleware** 的形式实现在 Booking Service 侧（不得嵌入业务逻辑）
- 参数（错误阈值、超时时长、时间窗口）通过**环境变量**配置
- **日志**中可观察到状态转换（`CLOSED → OPEN`、`OPEN → HALF_OPEN` 等）
- 处于 OPEN 状态时，客户端收到**明确的错误响应**（如 `503 Service Unavailable`），而非超时

---

## 📋 附录一：方法规范

### Booking Service（REST API）

#### `GET /flights?origin=SVO&destination=LED&date=2026-04-01` — 搜索航班
代理调用 Flight Service 的 `SearchFlights`。`origin` 和 `destination` 为必填参数，`date` 可选。返回状态为 `SCHEDULED` 的航班列表。

#### `GET /flights/{id}` — 获取航班
代理调用 Flight Service 的 `GetFlight`。若航班不存在，返回 `404`。

#### `POST /bookings` — 创建预订
请求体包含：`user_id`、`flight_id`、`passenger_name`、`passenger_email`、`seat_count`。

必须按以下顺序执行：
1. 调用 `GetFlight` → 若航班不存在则返回错误
2. 调用 `ReserveSeats` → 若座位不足则返回错误
3. 快照价格：`total_price = seat_count × flight.price`（锁定预订时刻的价格）
4. 以 `CONFIRMED` 状态写入数据库

> 若座位锁定失败，**不得创建**预订记录。

#### `GET /bookings/{id}` — 获取预订
按 ID 返回预订信息。若不存在，返回 `404`。

#### `POST /bookings/{id}/cancel` — 取消预订
1. 校验预订状态为 `CONFIRMED`
2. 调用 `ReleaseReservation` 释放座位 *(3 分方案可跳过此步)*
3. 将状态更新为 `CANCELLED`

#### `GET /bookings?user_id=X` — 预订列表
返回指定用户的所有预订记录。

---

### Flight Service（gRPC）

契约由学生自行设计，以下为各方法的行为说明。

#### `SearchFlights`
按路线（`origin`、`destination`）及可选日期搜索航班，返回状态为 `SCHEDULED` 的航班列表。

#### `GetFlight`
按 ID 获取航班信息。若不存在，返回 `NOT_FOUND`。

#### `ReserveSeats`
接收 `flight_id`、`seat_count`、`booking_id`，原子性地减少 `available_seats` 并创建 `SeatReservation`。若座位不足，返回 `RESOURCE_EXHAUSTED`。

#### `ReleaseReservation`
接收 `booking_id`，查找对应的活跃预留记录，归还座位，并将状态更新为 `RELEASED`。