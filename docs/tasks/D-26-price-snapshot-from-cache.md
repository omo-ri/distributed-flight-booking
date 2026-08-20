---
id: D-26
title: 订单金额快照读的是缓存里的价格
severity: minor
status: todo
phase: backlog
blocks: []
refs:
  - booking-service/internal/service/booking.go
  - flight-service/internal/service/flight.go
  - proto/flight/flight.proto
---

# D-26 订单金额快照读的是缓存里的价格

**位置**：`booking-service/internal/service/booking.go:150`

## 现象 / 触发场景

`bookings.total_price` 是下单时刻的价格快照——这个设计是对的（航班改价后已有订单金额不能跟着变）。但快照的**数据来源**有问题：

```go
flightResp, err := s.flights.GetFlight(ctx, req.FlightID)   // :103
flight := flightResp.Flight
...
totalPrice := int64(req.SeatCount) * flight.Price           // :150  ← flight.Price 来自缓存
```

`GetFlight` 走的是 Cache-Aside 读路径（`flight-service/internal/service/flight.go:54-79`），命中时直接返回 Redis 里的 JSON，**TTL 是 5 分钟**（`flight-service/internal/cache/redis.go:14`）。

所以：**航班改价后的 5 分钟内，新下的订单会按旧价成交并永久落库。**

缓存的显式失效帮不上忙——`invalidateFlightCache` 只在 `ReserveSeats` / `ReleaseReservation` 之后触发（`service/flight.go:88`、`:100`），也就是**只有库存变动才会失效缓存**。改价不经过这两条路径，所以不会触发任何失效，只能等 TTL 自然过期。

## 当前触发不了，但不能因此忽略

系统**没有任何一条代码路径会修改 `flights.price`**——航班数据只能靠 `flight-service/migrations/002_seed.up.sql` 写入。所以这个缺陷目前是休眠的。

登记它的两个理由：

1. **它会在增加航班管理能力的那一刻立即生效**，而那时候没人会想起来检查这里——改价功能的开发者关心的是"改价接口能不能用"，不会想到订单金额的取值链路
2. **它暴露的是一个设计层面的错配**：`total_price` 是财务数据，而它的输入来自一个**为读性能而存在、允许陈旧**的组件。这个错配和"改价路径存不存在"无关

## 根因

同一个 gRPC 调用被用于两个要求完全不同的目的：

| 用途 | 对新鲜度的要求 |
|---|---|
| 给用户展示航班信息、余位 | 宽松——5 分钟旧值可接受，这正是缓存存在的理由 |
| **计算订单金额** | **严格——必须是权威值，金额错了要赔钱** |

`CreateBooking` 复用了展示用的读路径来取财务数据，而没有意识到这条路径带缓存。

顺带一提，同一个调用返回的 `available_seats` 也是陈旧的（`service/booking.go:116`、`:139` 用它写日志和错误信息）——但那个不影响正确性，因为真正的余位校验发生在 flight-service 的事务内（`repository/flight.go:109-111`）。**同一份陈旧数据，用在校验上是安全的（因为有第二道权威校验），用在金额上是不安全的（因为没有第二道）。**

## 修法

**推荐**：`ReserveSeats` 直接返回权威价格。它本来就在事务里读了 `flights` 行（`repository/flight.go:99-101`），把 `price` 一起带回来几乎零成本，且这个值一定是**扣减那一刻**的权威值——比"下单前查一次"语义更准确。

改动范围：`proto/flight/flight.proto` 的 `ReserveSeatsResponse` 加字段 → `make proto` → `service/booking.go` 用它算 `totalPrice`。

**代价**：gRPC 契约变更（加字段是向后兼容的，proto 字段号只增不改）。

**备选**：给 `GetFlight` 加一个"跳过缓存"的参数，下单路径用它。**代价**：契约里多一个只有一个调用方会用的开关，而且下单路径会永远绕开缓存——尖峰时这条路径的 QPS 不低，全部落到数据库上会和写路径抢连接。**不推荐**。

**顺带能一起解决的问题**：改完之后，`CreateBooking` 里那次 `GetFlight`（`:103`）就只剩"检查航班是否存在"这一个作用了，而 `ReserveSeats` 本来就会返回 `NOT_FOUND`——**这一跳可以整个删掉**，省一次跨服务往返。这对 R2（延迟预算）是实打实的收益，尤其在 [D-25](./D-25-no-timeouts-anywhere.md) 加上超时之后，少一跳就是少一份超时预算。

## 验收标准

- 集成测试：直接改数据库里的 `flights.price` → 立刻下单 → `bookings.total_price` 使用**新价格**（当前会用旧价格直到 TTL 过期）
- 上述测试在缓存**命中**的前提下也通过（先查一次航班预热缓存，再改价，再下单）
- 现有的价格计算回归通过（正常下单金额 = 座位数 × 单价）
- 若一并删除了下单路径上的 `GetFlight` 调用：航班不存在时仍返回 404（改由 `ReserveSeats` 的 `NOT_FOUND` 提供）

## 学到什么

**缓存的正确性边界不由缓存本身决定，由每一个读取方的要求决定。** 同一份 5 分钟 TTL 的数据，用来渲染页面完全没问题，用来计算金额就是财务错误——而代码里它们是同一个函数调用，看不出区别。

一般化的判据：**取一个值之前，先问"这个值陈旧了会怎样"。** 答案是"用户看到的数字略旧"就可以走缓存；答案是"会产生一条永久错误的记录"就必须走权威源。

还有一条更具体的：**权威值应该在做出改变的那个事务里读取，而不是事先查好再带过去。** `ReserveSeats` 已经在事务里锁住并读了那一行，那里才是取价格的正确位置——事先查好的值，在你用它的时候已经可能过期了，无论中间有没有缓存。
