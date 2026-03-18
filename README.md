# 第3次课后作业
## 机票预订系统：gRPC + Redis

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