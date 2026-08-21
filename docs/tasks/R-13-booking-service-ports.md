---
id: R-13
title: booking-service 建立 ports.go 与 handler/errors.go（含 D-20 的 booking 侧）
severity: major
status: todo
phase: foundation
blocks: []
refs:
  - booking-service/internal/service/booking.go
  - booking-service/internal/handler/booking.go
  - docs/tasks/D-20-internal-error-disclosure.md
---

# R-13 booking-service 建立 ports.go 与 handler/errors.go（含 D-20 的 booking 侧）

**位置**：`booking-service/internal/service/booking.go`（211 行，无 `ports.go`）、`internal/handler/booking.go`（237 行，装了处理函数 + 错误映射 + 参数解析三件事）。

**第 3 步的收尾，也是整个第 2、3 步的 contract 阶段**——[R-10](./R-10-booking-domain-package.md) 留下的兼容别名在这里删干净。

## 为什么需要

前面几条把类型和错误的**流向**理顺了，但两件事还没做：

1. **service 依赖的仍是具体类型**——`*repository.BookingRepo` 和 `*grpcclient.FlightClient`。想给业务规则写 L1 单测，仍然要么起数据库、要么起 gRPC 服务
2. **handler 装了三件事**，237 行里错误映射和参数解析各占一块。按拆分阈值判（能不能独立说清，不按行数判）：它讲不清了，拆的边界是那三件事的分界

## 做什么

**1. 新增 `service/ports.go`** —— 这个包的目录：

```go
// 对外提供什么
type BookingService interface {
    CreateBooking(ctx context.Context, in CreateBookingInput) (domain.Booking, error)
    CancelBooking(ctx context.Context, id string) (domain.Booking, error)
    // ...
}

// 需要什么工具
type BookingRepo interface {
    Create(ctx context.Context, b domain.Booking) (domain.Booking, error)
    GetByID(ctx context.Context, id string) (domain.Booking, error)
    UpdateStatus(ctx context.Context, id string, s domain.BookingStatus) error
}

type FlightGateway interface {
    GetFlight(ctx context.Context, id string) (domain.Flight, error)
    ReserveSeats(ctx context.Context, flightID string, n int32, bookingID string) error
    ReleaseReservation(ctx context.Context, bookingID string) error
}
```

**接口写在用它的那一层。** `repository` 与 `grpcclient` 不定义这些接口，它们只是碰巧满足。

**不要过度倒置**：`logctx` 直接 import（任何环境下都是同一个 context 字段袋），`uuid.New()` 不抽接口（除非哪天要测确定性 ID）。判据是**这个依赖会不会因为环境不同而需要替换**。

**2. 新增 `handler/errors.go`**：领域错误 → HTTP 状态码的映射表从 `booking.go` 里搬出来。现有的 `errors.Is` 写法方向是对的，只是位置该独立。

**3. D-20 的 booking 侧**：内部错误原文只进日志（`logctx.Fail`），对外统一泛化消息 + `trace_id`。`handler/booking.go:37` 现在的 `logctx.Add(ctx, KeyError, err.Error())` 改走 [R-01](./R-01-logctx-semantic-wrappers.md) 的 `Fail`。

**4. 删掉 [R-10](./R-10-booking-domain-package.md) 留下的兼容别名**，`service` 包不再重新导出任何领域错误。

**5. `InsufficientSeatsError` 用 `errors.As` 取出可用座位数**，挂进汇总行并写进 API 响应——这是从 [R-06](./R-06-flight-domain-package.md) 一路传过来的那个数字第一次真正被用上。

## 验收标准

- **怎么验证它生效了**：`service/ports.go` 存在；`bookingService` 结构体字段类型全是本包定义的接口；`grep -rn "grpcclient\.\|repository\." internal/service/*.go` 零命中（构造函数除外）
- L1 单测：用内存假实现填两个端口，**不起数据库、不起 gRPC**，跑通 `CreateBooking` 的成功路径与「座位不足」「航班不存在」「预留成功但写库失败」三条分支
- 单测：注入含表名 / 连接串的数据库错误，断言它进日志、**不进响应体**；响应体里有 `trace_id`
- 单测：座位不足时响应 409，汇总行 `outcome=rejected`、`reason=insufficient_seats`、`available=N`，**级别是 `Info`**（不是 Warn——没人需要为一次座位不足做什么）
- `grep -rn "domain\." internal/service/booking.go` 里不再出现别名转发
- pytest 全绿 + `docker compose up` 行为不变
- **怎么回滚**：单个 commit revert；对外只有错误消息变泛化

## 注意事项

做完这条，[T-03](./T-03-flight-l2-tests.md) / [T-04](./T-04-booking-l2-tests.md) 的描述要跟着改：一批断言可以从 L2 降到 L1（「能在上一档证明的事，不许放到下一档」，`CLAUDE.md` § 4）。**降档不是删测试**——L2 保留真实 SQL、并发事务这些只有真数据库能证明的部分。

## 学到什么

**contract 阶段最容易被跳过**，因为兼容别名留着不碍事、编译也过。但留着的代价是：**同一个错误有两个名字**，而「哪个是正的」只有写代码的人知道。expand–contract 的价值在 contract 那一步才兑现——不删旧的，就等于把两套并存的复杂度永久留在了代码里。

回头看整个第 2、3 步：真正被消除的不是行数，是**「读一条业务规则要同时理解三种技术细节」**。改完之后 `ports.go` 二十来行能回答「这一层能干什么、需要什么」——而这正是最初那个挫败感（打开自己的代码库，不知道从哪开始写）的解药。
