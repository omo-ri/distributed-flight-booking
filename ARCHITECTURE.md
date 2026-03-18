# 项目架构与实现说明（答辩参考）

## 一、系统架构总览

```
客户端 (HTTP REST)
       │
       ▼
┌─────────────────┐      gRPC (port 50051)     ┌─────────────────┐
│ Booking Service │ ──────────────────────────► │ Flight Service  │
│   (Echo HTTP)   │    带 x-api-key 认证        │   (gRPC Server) │
│   port 8080     │                             │                 │
└────────┬────────┘                             └────────┬────────┘
         │                                               │
    PostgreSQL                                      PostgreSQL
    (booking_db)                                    (flight_db)
                                                         │
                                                      Redis
                                                    (缓存层)
```

整个系统用 **Go 语言**实现，通过 `docker-compose up` 一条命令启动 **5 个容器**：

| 容器 | 作用 | 端口 |
|------|------|------|
| `flight-db` | Flight Service 的 PostgreSQL | 5433 |
| `booking-db` | Booking Service 的 PostgreSQL | 5434 |
| `redis` | Flight Service 的缓存 | 6379 |
| `flight-service` | gRPC 服务（内部服务） | 50051 |
| `booking-service` | HTTP REST 服务（面向客户端） | 8080 |

---

## 二、项目目录结构

```
hw3/
├── proto/flight/flight.proto          # gRPC 接口定义（protobuf）
├── docker-compose.yml                 # 容器编排，一键启动所有服务
├── Makefile                           # 常用命令快捷方式
│
├── flight-service/                    # 航班服务（gRPC）
│   ├── cmd/main.go                    # 启动入口
│   ├── internal/
│   │   ├── handler/flight.go          # gRPC handler（接收请求、格式转换）
│   │   ├── service/flight.go          # 业务逻辑层（含缓存读写）
│   │   ├── repository/
│   │   │   ├── postgres.go            # 数据库连接、连接池、事务封装
│   │   │   └── flight.go              # SQL 查询（CRUD、事务锁座）
│   │   ├── auth/interceptor.go        # gRPC 认证拦截器
│   │   └── cache/redis.go             # Redis 缓存操作
│   ├── pb/flight/                     # protoc 自动生成的 Go 代码
│   ├── migrations/                    # 数据库迁移脚本
│   └── Dockerfile                     # 多阶段构建
│
├── booking-service/                   # 预订服务（HTTP REST）
│   ├── cmd/main.go                    # 启动入口
│   ├── api/
│   │   ├── openapi.yaml               # OpenAPI 3.0 接口定义
│   │   └── api.gen.go                 # oapi-codegen 自动生成的路由代码
│   ├── internal/
│   │   ├── handler/booking.go         # HTTP handler（Echo 路由处理）
│   │   ├── service/booking.go         # 业务逻辑层（编排 gRPC 调用 + DB 操作）
│   │   ├── repository/
│   │   │   ├── postgres.go            # 数据库连接（同 flight-service）
│   │   │   └── booking.go             # SQL 查询（CRUD）
│   │   └── grpcclient/flight.go       # gRPC 客户端（调用 Flight Service）
│   ├── migrations/                    # 数据库迁移脚本
│   └── Dockerfile                     # 多阶段构建
│
└── tests/                             # pytest 集成测试
    ├── conftest.py
    └── test_api.py
```

---

## 三、两个服务的分层架构

两个服务都采用经典的**三层架构**（Clean Architecture）：

```
main.go (启动入口：初始化所有依赖、注入、启动服务器)
  └── handler  (接收请求 → 参数校验 → 调用 service → 格式转换 → 返回响应)
       └── service  (业务逻辑：编排多个操作的顺序、处理缓存)
            └── repository  (纯数据库操作：SQL 查询、事务)
```

**为什么分层？**
- handler 只管请求/响应格式，不知道数据库怎么存
- service 只管业务流程，不关心是 HTTP 还是 gRPC
- repository 只管 SQL，不关心业务规则
- 方便测试，每层可以独立替换

---

## 四、逐题实现详解

### 第1题：Flight Service 的 gRPC 契约

**文件**：`proto/flight/flight.proto`

定义了 Flight Service 的全部接口：

**两个枚举（enum）**：
- `FlightStatus`：`SCHEDULED`(已排期) / `DEPARTED`(已起飞) / `CANCELLED`(已取消) / `COMPLETED`(已完成)
- `ReservationStatus`：`ACTIVE`(生效中) / `RELEASED`(已释放) / `EXPIRED`(已过期)

> 枚举的第一个值都是 `UNSPECIFIED = 0`，这是 protobuf 的最佳实践，防止零值被误解释。

**两个核心消息体（message）**：
- `Flight` — 航班：UUID、航班号、航空公司、起降 IATA 码、时间（用 `google.protobuf.Timestamp`）、座位数、价格（int64 最小单位分/戈比）、状态
- `SeatReservation` — 座位预留记录

**四个 RPC 方法**：

| 方法 | 输入参数 | 返回值 | 业务描述 |
|------|----------|--------|----------|
| `SearchFlights` | origin, destination, date(可选) | Flight 列表 | 按航线和日期搜索状态为 SCHEDULED 的航班 |
| `GetFlight` | id (UUID) | 单个 Flight | 按 ID 获取航班，不存在返回 NOT_FOUND |
| `ReserveSeats` | flight_id, seat_count, booking_id | SeatReservation | 原子锁座，座位不足返回 RESOURCE_EXHAUSTED |
| `ReleaseReservation` | booking_id | SeatReservation | 释放座位，找不到活跃预留返回 NOT_FOUND |

**编译命令**（`make proto`）：
```bash
protoc --go_out=. --go-grpc_out=. proto/flight/flight.proto
```
生成 `pb/flight/flight.pb.go`（消息序列化）和 `flight_grpc.pb.go`（gRPC 客户端/服务端桩代码）。

---

### 第2题：数据库设计（符合 3NF）

#### Flight Service 数据库

**`flights` 表**（`flight-service/migrations/001_init.up.sql`）：

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | UUID | PK, 默认 gen_random_uuid() | 主键 |
| flight_number | VARCHAR(10) | NOT NULL | 航班号，如 SU1234 |
| airline | VARCHAR(100) | NOT NULL | 航空公司 |
| origin | VARCHAR(3) | NOT NULL | 出发机场 IATA 码 |
| destination | VARCHAR(3) | NOT NULL | 到达机场 IATA 码 |
| departure_time | TIMESTAMP | NOT NULL | 起飞时间 |
| arrival_time | TIMESTAMP | NOT NULL | 到达时间 |
| total_seats | INT | NOT NULL, CHECK > 0 | 总座位数 |
| available_seats | INT | NOT NULL, CHECK >= 0, CHECK <= total_seats | 可用座位数 |
| price | BIGINT | NOT NULL, CHECK > 0 | 票价（最小货币单位） |
| status | VARCHAR(20) | NOT NULL, CHECK IN (...) | 航班状态 |

**索引**：
- `UNIQUE(flight_number, departure_time::date)` — 同一天同一航班号不重复
- `INDEX(origin, destination, departure_time::date)` — 加速按航线+日期搜索

**`seat_reservations` 表**：

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | UUID | PK | 预留记录 ID |
| flight_id | UUID | FK → flights(id) | 关联航班 |
| booking_id | UUID | UNIQUE | 关联预订（一个预订只有一条预留） |
| seat_count | INT | CHECK > 0 | 预留座位数 |
| status | VARCHAR(20) | CHECK IN (...) | ACTIVE / RELEASED / EXPIRED |
| created_at | TIMESTAMP | 默认 NOW() | 创建时间 |

#### Booking Service 数据库

**`bookings` 表**（`booking-service/migrations/001_init.up.sql`）：

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | UUID | PK | 预订 ID |
| user_id | UUID | NOT NULL | 用户 ID |
| flight_id | UUID | NOT NULL | 关联的航班 ID（跨服务引用） |
| passenger_name | VARCHAR(200) | NOT NULL | 乘客姓名 |
| passenger_email | VARCHAR(200) | NOT NULL | 乘客邮箱 |
| seat_count | INT | CHECK > 0 | 预订座位数 |
| total_price | BIGINT | CHECK > 0 | 总价（锁定时刻的价格快照） |
| status | VARCHAR(20) | CHECK IN (...) | CONFIRMED / CANCELLED |
| created_at | TIMESTAMPTZ | 默认 NOW() | 创建时间 |

**索引**：`INDEX(user_id)` — 加速按用户查预订列表

> **3NF 说明**：每个表的非主键列都直接依赖于主键（没有传递依赖），`flight_id` 在 bookings 中只存引用 ID 而非冗余航班信息（除了 total_price 是刻意的价格快照）。

---

### 第3题：PostgreSQL + 两个服务实现

#### 数据库连接层

**文件**：两个服务各自的 `internal/repository/postgres.go`

```go
// 连接配置
type PostgresConfig struct {
    Host, Port, User, Password, DBName, SSLMode string
}
// 拼接 DSN: postgres://user:pass@host:port/db?sslmode=disable
func (c PostgresConfig) DSN() string { ... }

// 数据库实例（包含连接池）
type DB struct { Pool *pgxpool.Pool }
// 创建连接池 + Ping 检测
func NewDB(ctx, cfg) (*DB, error) { ... }
// 事务辅助方法：自动 Begin → 执行 fn → Commit 或 Rollback
func (db *DB) WithTx(ctx, fn) error { ... }
```

- 使用 **pgxpool** 连接池，不是每次请求建立新连接
- `WithTx` 封装了事务模板：开始事务 → 执行传入的函数 → 成功就 Commit，失败就 Rollback

#### 数据库迁移

两个服务的 `main.go` 启动时都调用 `runMigrations()`：
```go
func runMigrations(cfg PostgresConfig) error {
    m, _ := migrate.New("file://migrations", cfg.DSN())
    m.Up()  // 执行 migrations/ 目录下的 SQL 文件
}
```
使用 **golang-migrate** 库，自动执行 `001_init.up.sql` 等迁移文件。

#### Booking Service REST API

使用 **oapi-codegen** 从 `api/openapi.yaml`（OpenAPI 3.0 规范）自动生成路由代码 `api/api.gen.go`。

生成的 `ServerInterface` 接口定义了所有要实现的方法签名，`BookingHandler` 实现这个接口：
```go
var _ api.ServerInterface = (*BookingHandler)(nil) // 编译时检查接口实现
```

HTTP 框架用 **Echo v4**，注册路由只需一行：
```go
api.RegisterHandlers(e, h)
```

提供的 REST 接口：

| 路径 | 方法 | 功能 |
|------|------|------|
| `GET /flights?origin=X&destination=Y&date=Z` | GET | 搜索航班（代理 gRPC） |
| `GET /flights/{id}` | GET | 获取航班详情（代理 gRPC） |
| `POST /bookings` | POST | 创建预订 |
| `GET /bookings/{id}` | GET | 获取预订详情 |
| `GET /bookings?user_id=X` | GET | 用户预订列表 |
| `POST /bookings/{id}/cancel` | POST | 取消预订 |

---

### 第4题：gRPC 服务间通信

**Booking Service 通过 gRPC 调用 Flight Service**。

#### gRPC 客户端封装

**文件**：`booking-service/internal/grpcclient/flight.go`

```go
type FlightClient struct {
    conn   *grpc.ClientConn         // gRPC 连接
    client pb.FlightServiceClient   // protoc 生成的客户端
    apiKey string                   // 认证密钥
}
```

- `NewFlightClient(addr, apiKey)` — 建立 gRPC 连接（用 insecure 因为内网通信）
- `withAuth(ctx)` — 把 API Key 注入 gRPC metadata：`metadata.AppendToOutgoingContext(ctx, "x-api-key", apiKey)`
- 每个方法（SearchFlights/GetFlight/ReserveSeats/ReleaseReservation）都调用 `withAuth` 带上认证

#### 创建预订的完整流程

**文件**：`booking-service/internal/service/booking.go` → `CreateBooking` 方法

```
步骤1：调用 GetFlight(flight_id)
        → 获取航班信息，主要是拿到 price（票价）
        → 如果航班不存在，返回 NOT_FOUND → 客户端收到 404

步骤2：生成 booking_id = uuid.New()
        → 提前生成 UUID，传给 ReserveSeats 用于幂等性

步骤3：调用 ReserveSeats(flight_id, seat_count, booking_id)
        → 在 Flight Service 侧原子锁座
        → NOT_FOUND → 404
        → RESOURCE_EXHAUSTED → 409 座位不足
        → 其他错误 → 500
        → ❗ 锁座失败 = 不创建预订记录，直接返回错误

步骤4：计算 total_price = seat_count × flight.price
        → 价格快照：用的是预订时刻的价格，后续航班改价不影响已有预订

步骤5：写入 bookings 表，状态 CONFIRMED
        → INSERT 带上提前生成的 booking_id
```

#### 取消预订的流程

```
步骤1：从 bookings 表查预订 → 不存在返回 404
步骤2：检查状态 → 已是 CANCELLED 返回 409
步骤3：调用 ReleaseReservation(booking_id) → 释放 Flight Service 的座位
步骤4：UPDATE bookings SET status = 'CANCELLED'
```

---

### 第5题：事务一致性

#### Flight Service 的 ReserveSeats（锁座）

**文件**：`flight-service/internal/repository/flight.go` → `ReserveSeats` 方法

在**一个数据库事务**内完成以下操作：

```
BEGIN TRANSACTION

  1. 幂等性检查：
     SELECT ... FROM seat_reservations WHERE booking_id = ? AND status = 'ACTIVE'
     → 如果已存在，直接返回已有记录（不重复扣座）

  2. 行级锁定：
     SELECT available_seats FROM flights WHERE id = ? FOR UPDATE
     → FOR UPDATE 锁定这一行，其他事务必须等这个事务结束才能读写
     → 防止两个请求同时预订最后一个座位（竞态条件）

  3. 检查座位是否足够：
     if available < seatCount → 返回 ErrInsufficientSeats

  4. 扣减座位：
     UPDATE flights SET available_seats = available_seats - ? WHERE id = ?

  5. 创建预留记录：
     INSERT INTO seat_reservations (flight_id, booking_id, seat_count, status='ACTIVE')

COMMIT（以上 4 和 5 要么一起成功，要么一起失败）
```

> **关键**：`SELECT FOR UPDATE` 是行级悲观锁。假设有 1 个座位，A 和 B 同时预订：A 先拿到锁 → 扣座成功 → 提交 → B 才能读到 available_seats=0 → 返回座位不足。

#### Flight Service 的 ReleaseReservation（释放座位）

同样在一个事务内：
```
BEGIN TRANSACTION
  1. 查找 ACTIVE 状态的预留记录
  2. UPDATE flights SET available_seats + seat_count（归还座位）
  3. UPDATE seat_reservations SET status = 'RELEASED'
COMMIT
```

#### Booking Service 侧的一致性

- 如果 `ReserveSeats` gRPC 调用失败 → `CreateBooking` 直接返回错误，**不会执行** `INSERT INTO bookings`
- 不存在"座位扣了但预订没建"或"预订建了但座位没扣"的中间状态

---

### 第6题：服务间认证（API Key 方案）

#### Flight Service 侧 — 拦截器

**文件**：`flight-service/internal/auth/interceptor.go`

```go
func UnaryInterceptor(apiKey string) grpc.UnaryServerInterceptor {
    return func(ctx, req, info, handler) {
        // 1. 从 gRPC metadata 中提取 "x-api-key"
        md := metadata.FromIncomingContext(ctx)
        keys := md.Get("x-api-key")

        // 2. 校验
        if len(keys) == 0 || keys[0] != apiKey {
            return codes.Unauthenticated  // 没有 key 或 key 错误
        }

        // 3. 通过 → 继续处理请求
        return handler(ctx, req)
    }
}
```

- 这是一个 **gRPC Unary Interceptor**（一元拦截器），相当于 HTTP 中间件
- 注册在 gRPC Server 上，**所有方法**都会经过它
- 日志记录 `[AUTH] OK` 或 `[AUTH] REJECTED`

#### Booking Service 侧 — 注入认证

**文件**：`booking-service/internal/grpcclient/flight.go`

```go
func (c *FlightClient) withAuth(ctx context.Context) context.Context {
    return metadata.AppendToOutgoingContext(ctx, "x-api-key", c.apiKey)
}
```

每次 gRPC 调用前，把 API Key 塞进 metadata（类似 HTTP Header）。

#### 密钥传递方式

在 `docker-compose.yml` 中通过**环境变量**传入：
```yaml
flight-service:
  environment:
    AUTH_API_KEY: "super-secret-key"

booking-service:
  environment:
    AUTH_API_KEY: "super-secret-key"  # 与 flight-service 一致
```

---

### 第7题：Redis 缓存

#### 缓存结构

**文件**：`flight-service/internal/cache/redis.go`

| 数据类型 | Key 格式 | TTL | 示例 |
|----------|----------|-----|------|
| 单个航班 | `flight:{id}` | 5 分钟 | `flight:a0eebc99-...` |
| 搜索结果 | `search:{origin}:{destination}:{date}` | 5 分钟 | `search:SVO:LED:2026-04-01` |

提供的方法：
- `GetFlight` / `SetFlight` / `InvalidateFlight` — 航班缓存读/写/删
- `GetSearch` / `SetSearch` / `InvalidateSearchByFlight` — 搜索缓存读/写/删
- `MarshalJSON` / `UnmarshalJSON` — 序列化辅助

#### Cache-Aside 策略

**文件**：`flight-service/internal/service/flight.go`

**读操作**（SearchFlights、GetFlight）：
```
1. 先查 Redis → 命中（HIT）→ 反序列化后直接返回
2. 未命中（MISS）→ 查 PostgreSQL
3. 查到后 → 序列化写入 Redis，设置 5 分钟 TTL
4. 返回结果
```

**写操作**（ReserveSeats、ReleaseReservation）：
```
1. 先执行数据库操作（锁座/释放座位）
2. 操作成功后，主动失效缓存：
   a. 删除 flight:{id}（该航班的缓存）
   b. 查出航班的 origin/destination/date
   c. 删除 search:{origin}:{destination}:{date}（相关搜索缓存）
   d. 删除 search:{origin}:{destination}:（无日期的搜索缓存）
```

#### 日志

每次缓存操作都有明确日志：
```
[CACHE] HIT   flight:a0eebc99-...
[CACHE] MISS  search:SVO:LED:2026-04-01
[CACHE] SET   flight:a0eebc99-... (ttl=5m0s)
[CACHE] DEL   flight:a0eebc99-...
```

#### Redis 可选

`main.go` 中检查 `REDIS_ADDR` 环境变量：
- 有值 → 连接 Redis，启用缓存
- 无值 → `cache` 字段为 nil，service 层所有缓存逻辑被跳过，服务照常运行

---

## 五、技术栈总结

| 组件 | 技术选型 | 说明 |
|------|----------|------|
| 编程语言 | Go | |
| HTTP 框架 | Echo v4 | 轻量高性能 |
| API 规范 | OpenAPI 3.0 + oapi-codegen | 从 YAML 自动生成路由和类型 |
| RPC 框架 | gRPC + Protocol Buffers | 服务间通信 |
| 代码生成 | protoc + protoc-gen-go-grpc | 从 .proto 生成 Go 代码 |
| 数据库驱动 | pgx v5 + pgxpool | 高性能 PostgreSQL 驱动 + 连接池 |
| 数据库迁移 | golang-migrate | 启动时自动执行 SQL 迁移 |
| 缓存 | go-redis v9 | Redis 客户端 |
| UUID | google/uuid | 生成预订 ID |
| 日志 | slog（Booking）/ log（Flight） | Booking 用结构化 JSON，Flight 用标准库 |
| 容器化 | Docker 多阶段构建 + docker-compose | 一键部署 |
| 测试 | pytest + requests | Python 集成测试 |

---

## 六、答辩常见问题与回答

### Q1：为什么用 `SELECT FOR UPDATE`？
**A**：这是行级悲观锁。当两个请求同时预订同一航班时，先到的事务锁定航班行，后到的事务必须等待。防止两个请求同时读到"剩 1 个座位"然后都扣座成功导致超卖。

### Q2：为什么 booking_id 要提前生成？
**A**：用于 ReserveSeats 的**幂等性**。如果 gRPC 调用因网络超时重试，Flight Service 先查 `booking_id` 是否已有 ACTIVE 预留，有就直接返回旧记录而不重复扣座。

### Q3：缓存失效策略是什么？
**A**：**Cache-Aside + 主动失效**。读请求走"查缓存→miss→查DB→写缓存"流程。写请求（锁座/释放）完成后立即删除相关缓存 key，下次读会从 DB 重新加载。所有 key 都有 5 分钟 TTL 兜底。

### Q4：为什么两个服务各有独立数据库？
**A**：微服务架构原则——每个服务拥有自己的数据存储，服务之间只通过 API（gRPC）通信。这样服务可以独立部署、独立扩展，数据库 schema 变更不会影响其他服务。

### Q5：认证是怎么实现的？
**A**：使用 API Key 方案。Booking Service 在每次 gRPC 调用时把 key 放入 metadata 的 `x-api-key` 头。Flight Service 用 gRPC Unary Interceptor 拦截所有请求，校验 key，失败返回 `UNAUTHENTICATED`。密钥通过 docker-compose 环境变量注入。

### Q6：如果锁座成功但写入 bookings 表失败会怎样？
**A**：这是一个已知的边界情况。目前座位会被锁住但没有对应的预订记录。更完善的方案是加补偿逻辑（座位预留超时自动释放），这属于第 8-10 题的高可用范围。

### Q7：Cache-Aside 模式有什么缺点？
**A**：在缓存失效和 DB 写入之间有短暂的不一致窗口。比如刚锁了座位还没删缓存时，另一个请求可能读到旧的 available_seats。但因为实际的座位扣减有数据库事务保护，所以不会超卖——最坏情况是缓存显示"有座位"但实际预订时返回"座位不足"。

### Q8：为什么 Flight Service 用标准 log，Booking Service 用 slog？
**A**：Booking Service 面向外部，用结构化 JSON 日志便于日志收集系统（如 ELK）解析。Flight Service 是内部服务，用标准库 log 足够。实际生产中应统一。

### Q9：gRPC 和 REST 的区别？为什么服务间用 gRPC？
**A**：gRPC 用 Protocol Buffers 二进制序列化（比 JSON 小、快），基于 HTTP/2 支持多路复用，有强类型的接口定义（.proto），自动生成客户端代码。适合微服务间高频内部通信。REST 适合面向外部客户端，因为浏览器/curl 原生支持。

### Q10：数据库迁移是怎么工作的？
**A**：用 golang-migrate 库，在 `main.go` 启动时调用 `migrate.Up()`。它读取 `migrations/` 目录下的 SQL 文件（按编号顺序执行），并在数据库中记录已执行的版本号，避免重复执行。
