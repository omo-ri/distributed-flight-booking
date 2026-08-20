---
id: D-24
title: POST /bookings 没有幂等保护，客户端重发会重复扣库存
severity: critical
status: todo
phase: 0
blocks: []
refs:
  - booking-service/internal/service/booking.go
  - booking-service/api/openapi.yaml
  - flight-service/migrations/001_init.up.sql
---

# D-24 POST /bookings 没有幂等保护，客户端重发会重复扣库存

**位置**：`booking-service/internal/service/booking.go:120`

## 现象 / 触发场景

`seat_reservations.booking_id` 上有 `UNIQUE` 约束，这让 `ReserveSeats` 具备幂等性——这部分是对的，而且是 gRPC 重试能安全存在的前提。

**但幂等键是在 `CreateBooking` 内部临时生成的**：

```go
func (s *bookingService) CreateBooking(ctx context.Context, req CreateBookingInput) (...) {
    ...
    bookingID := uuid.New().String()      // ← :120 每次调用都是新的
    ...
    _, err = s.flights.ReserveSeats(ctx, req.FlightID, req.SeatCount, bookingID)   // :128
```

所以幂等的作用域**只有一次 `CreateBooking` 调用内部**——即 `grpcclient` 的那 3 次自动重试。

客户端层面完全没有保护：

```
客户端 POST /bookings → 网关超时 / 网络抖动 / 用户没看到响应
客户端重发 POST /bookings
  → 走进一个全新的 CreateBooking
  → uuid.New() 生成一个全新的 booking_id
  → flight-service 的幂等前置检查（repository/flight.go:86-92）查不到这个新 ID
  → 正常扣减库存、正常插入预留、正常建订单
⇒ 用户被扣了两次座位、产生两个订单，而数据库层面这完全合法
   （两个不同的 booking_id，UNIQUE 约束毫无意见）
```

**在放票尖峰下这会被反复踩中**：用户看到页面转圈就会狂点提交按钮，而那恰恰是系统最慢、最容易超时的时刻。参见 `docs/design/system-design.md` § 1.3 的尖峰场景（60 秒内 2000 QPS 砸在一个航班上）。

## 根因

幂等键由**被调用方的调用方**生成，而不是由**最初的发起方**生成。

幂等设计的通用规则是：**幂等键必须由重试的那一方生成并在重试时保持不变。** 这里有两层重试：

| 重试发起方 | 幂等键 | 是否保持不变 | 幂等成立？ |
|---|---|---|---|
| `grpcclient` 的自动重试 | `bookingID` | ✅ 同一次调用内不变 | ✅ |
| **客户端重发 HTTP 请求** | `bookingID` | ❌ 每次都重新生成 | ❌ |

HTTP 这一层根本没有幂等键这个概念存在。

## 修法

`POST /bookings` 接受 `Idempotency-Key` 请求头（Stripe / PayPal 等采用的事实标准），booking-service 用它作为去重依据：

1. 请求带 `Idempotency-Key: <客户端生成的 UUID>`
2. booking-service 先查该 key 是否已有结果——命中则**直接返回已有订单**，不调用 flight-service
3. 未命中则正常执行，并把 `(key → booking_id)` 持久化

**存储位置的两个选择**：

| 做法 | 代价 |
|---|---|
| `bookings` 表加 `idempotency_key UUID UNIQUE` 列 | 简单，和订单同事务；但只覆盖"成功建单"的情况——如果失败在跳 8 之前（见 § 9.1），key 不会被记录，重发仍会重复扣库存 |
| 独立的幂等记录表，请求进来先占位 | 覆盖完整，但要处理"占位了但最终失败"的状态，以及占位记录的过期清理 |

**还要明确定义的语义**（不定义就会变成新的 bug）：

- **同 key 但请求体不同** → 返回 409，不能静默返回旧结果
- **key 的保留期** → 通常 24 小时；过期后同 key 视为新请求
- **并发的同 key 请求** → 靠 `UNIQUE` 约束让其中一个失败，失败方查出已有结果返回

**退化方案**（当无法要求客户端配合时）：用 `(user_id, flight_id, seat_count)` 加短时间窗口去重。**代价**：会误杀"同一个人真的想连着买两次同样的票"这种合法操作——用正确性换便利性，需要业务确认。

## 验收标准

- 带同一个 `Idempotency-Key` 连续发两次 `POST /bookings` → 只创建一个订单，`available_seats` 只减一次，两次响应体相同
- **并发**发送 10 个同 key 请求 → 只创建一个订单，其余返回同一个订单（或明确的 409）
- 同 key 但 `seat_count` 不同 → **409**
- 不带 `Idempotency-Key` 的请求仍能正常下单（向后兼容），或者明确改为必填并同步更新 `openapi.yaml`
- `openapi.yaml` 里声明了这个请求头及其语义

## 学到什么

**幂等的作用域等于幂等键的生命周期，一秒都不多。** 这个仓库里 `booking_id UNIQUE` 是个正确的设计，`ReserveSeats` 确实是幂等的——但它保护的范围止于 gRPC 那一跳。往上一层看，HTTP 入口毫无保护，而那正是重试最频繁发生的地方（用户、浏览器、网关、CDN 都会重试）。

判断"这个接口幂等吗"，要先问"谁会重试、它重试时带的标识是什么、那个标识跨重试保持不变吗"。三个问题任意一个答不上来，幂等就不成立。

同时这条解释了为什么 `docs/design/system-design.md` § 5.5 的清单里，四个 ✅ 全在单个数据库事务内部，而 ❌ 全都跨越了进程边界——**每跨一层边界，都要重新论证一次正确性，不能继承。**
