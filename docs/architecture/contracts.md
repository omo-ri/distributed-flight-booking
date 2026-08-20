# 接口契约

> **契约是源，代码是产物。** 改接口必须先改契约文件，再重新生成 —— 手改生成物不允许。

| 契约 | 源文件 | 生成物 | 生成命令 |
|---|---|---|---|
| gRPC（内部） | [`proto/flight/flight.proto`](../../proto/flight/flight.proto) | `flight-service/pb/flight/*.pb.go` | `make proto` |
| REST（对外） | [`booking-service/api/openapi.yaml`](../../booking-service/api/openapi.yaml) | `booking-service/api/api.gen.go` | oapi-codegen |

REST 侧的生成代码提供了 `api.ServerInterface`，handler 用一行编译期断言绑死：

```go
var _ api.ServerInterface = (*BookingHandler)(nil)
```

契约里加一个端点而 handler 没实现，**编译不过**。这比"记得同步改"可靠。

---

## REST API（booking-service，:8080）

对外只有这一个入口。flight-service 不暴露任何 HTTP 业务接口。

| 方法 | 路径 | 说明 | 下游 gRPC 调用 |
|---|---|---|---|
| GET | `/flights?origin=&destination=&date=` | 按航线搜索，`date` 可选 | `SearchFlights` |
| GET | `/flights/{id}` | 按 ID 取航班 | `GetFlight` |
| POST | `/bookings` | 创建订单 | `GetFlight` → `ReserveSeats` |
| GET | `/bookings/{id}` | 按 ID 取订单 | — |
| GET | `/bookings?user_id=` | 用户的订单列表 | — |
| POST | `/bookings/{id}/cancel` | 取消订单并归还座位 | `ReleaseReservation` |
| GET | `/metrics` | Prometheus 指标 | — |

### 创建订单的时序

```
POST /bookings
   │
   ├─1─► GetFlight(flight_id)              取价格；NOT_FOUND → 404
   │
   ├─2─  bookingID = uuid.New()            先生成 ID，让第 3 步可幂等重试
   │
   ├─3─► ReserveSeats(flight_id, n, bookingID)
   │        RESOURCE_EXHAUSTED → 409，不落库
   │
   ├─4─  total_price = n × flight.price    价格快照
   │
   └─5─  INSERT bookings (status=CONFIRMED)
```

第 2 步在调用前生成 `booking_id` 是关键设计：它让 `ReserveSeats` 携带一个稳定的幂等键，网络超时后重试不会重复扣座位。如果等到第 5 步由数据库生成 ID，重试就是危险的。

**已知缺口**：第 3 步成功、第 5 步失败时座位会泄漏，见 [D-06](../tasks/D-06-seat-inventory-leak.md)。

---

## gRPC 契约（flight-service，:50051）

四个方法，都是**业务操作**而不是表的 CRUD 包装 —— `ReserveSeats` 不是 "UpdateFlightSetAvailableSeats"，它表达的是"资源预留"这个领域概念，并且原子性由服务端保证，调用方无需知道底下有几张表。

| 方法 | 请求 | 语义 |
|---|---|---|
| `SearchFlights` | origin, destination, date? | 按航线查 `SCHEDULED` 状态的航班 |
| `GetFlight` | id | 按 ID 取单个航班 |
| `ReserveSeats` | flight_id, seat_count, **booking_id** | 原子地校验余位 + 扣减 + 建预留。同一 `booking_id` 幂等 |
| `ReleaseReservation` | booking_id | 找到活跃预留，归还座位，置为 `RELEASED` |

### 类型约定

- **时间**用 `google.protobuf.Timestamp`，不用字符串。唯一的例外是 `SearchFlightsRequest.date`，它是查询过滤条件而非时间点，用 `"2006-01-02"` 字符串
- **状态**用 enum，不用字符串：`FlightStatus`、`ReservationStatus`
- 每个 enum 的 0 值都是 `*_UNSPECIFIED` —— proto3 里 0 值是默认值，不占用它才能区分"没设置"和"设成了第一个合法值"
- **金额**用 `int64`，最小货币单位

### 错误码映射

业务错误映射到标准 gRPC code，再由 booking-service 映射到 HTTP：

| 业务情况 | gRPC code | HTTP | 代码位置 |
|---|---|---|---|
| 航班不存在 | `NOT_FOUND` | 404 | `flight-service/internal/handler/flight.go` |
| 活跃预留不存在 | `NOT_FOUND` | 404 | 同上 |
| 余位不足 | `RESOURCE_EXHAUSTED` | 409 | 同上 |
| 必填参数缺失 / 座位数 ≤ 0 | `INVALID_ARGUMENT` | 400 | 同上 |
| API Key 缺失或错误 | `UNAUTHENTICATED` | —（内部） | `flight-service/internal/auth/interceptor.go` |
| 服务端内部错误 | `INTERNAL` | 500 | 各 handler |
| 熔断器 OPEN | —（未发出调用） | 503 | `booking-service/internal/handler/booking.go` |

**`RESOURCE_EXHAUSTED` 而不是 `FAILED_PRECONDITION`**：余位不足是"资源被耗尽"，语义精确，且它在重试策略里被明确归类为**不可重试** —— 重试一百次座位也不会变多。

错误码的选择同时决定了三件事：客户端看到的 HTTP 状态、是否触发重试、是否计入熔断统计。第三件当前有缺陷，见 [D-01](../tasks/D-01-circuit-breaker-error-classification.md)。

---

## 服务间认证

API Key 走 gRPC metadata 的 `x-api-key` 字段，由 unary 拦截器统一校验，**覆盖全部方法**。

```
booking-service                          flight-service
  metadata.AppendToOutgoingContext  ──►   auth.UnaryInterceptor
      "x-api-key: $AUTH_API_KEY"            缺失/不匹配 → UNAUTHENTICATED
```

用拦截器而不是在每个 handler 里校验，意味着**新增 gRPC 方法不可能漏掉鉴权** —— 这是"安全默认开启"而非"记得加上"。

密钥通过 `AUTH_API_KEY` 环境变量注入两个服务。当前明文写在 compose 里，见 [D-07](../tasks/D-07-plaintext-secrets.md)；传输层未加密，见 [D-08](../tasks/D-08-grpc-mtls.md)。

---

## 契约演进

改契约时要能回答：**旧客户端会不会挂？**

| 变更 | 兼容性 | 
|---|---|
| 加新字段（新编号） | ✅ 向后兼容 |
| 加新方法 | ✅ 向后兼容 |
| 加新 enum 值 | ⚠️ 老客户端收到会落到未知值，需要有默认分支 |
| 改字段编号 / 改类型 | ❌ 破坏性 |
| 删字段 | ❌ 破坏性，正确做法是标 `reserved` |

滚动发布期间新旧版本会同时在线，破坏性变更必须走 expand-contract：先加新的、双写、迁移读、再删旧的。数据库 schema 同理，见 [plans/roadmap.md](../plans/roadmap.md) 阶段 3。
