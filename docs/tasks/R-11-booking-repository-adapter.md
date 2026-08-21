---
id: R-11
title: booking-service repository 改成出站适配器
severity: major
status: todo
phase: foundation
blocks: [R-13]
refs:
  - booking-service/internal/repository/booking.go
  - booking-service/internal/service/booking.go
---

# R-11 booking-service repository 改成出站适配器

**位置**：`booking-service/internal/repository/booking.go`（`BookingRow` 与查询方法）；跟随修改 `service/booking.go:39-46`、`handler/booking.go`。

## 为什么需要

service 的对外契约返回数据库行：

```go
type BookingService interface {
    CreateBooking(ctx context.Context, req CreateBookingInput) (repository.BookingRow, error)
    GetBooking(ctx context.Context, id string) (repository.BookingRow, error)
    ListBookings(ctx context.Context, userID string) ([]repository.BookingRow, error)
    CancelBooking(ctx context.Context, id string) (repository.BookingRow, error)
}
```

所以 handler 必须 `import repository`（`handler/booking.go:14` 确实这么做了）。数据库加一列、改个类型，涟漪一路推到 HTTP 响应。

与 flight 侧 [R-07](./R-07-flight-repository-adapter.md) 完全同构，照搬即可。

## 做什么

**1. 四个方法返回 `domain.Booking`**，`BookingRow` 降级成包内私有的扫描中转结构。

**2. 驱动错误在这里翻译**：`pgx.ErrNoRows` → `domain.ErrNotFound`，唯一约束冲突 → 对应领域错误。service 从此不知道 `pgx` 存在。

**3. service / handler 机械跟随，只换签名类型，不改逻辑。**

**4. 状态字符串在这里翻成 `domain.BookingStatus`**，反向写库时翻回去。

**本条不动 `grpcclient`**（归 [R-12](./R-12-booking-grpcclient-adapter.md)），**不动错误映射与 sentinel 别名**（归 [R-13](./R-13-booking-service-ports.md)）。

## 验收标准

- **怎么验证它生效了**：`grep -n "BookingRow" internal/service internal/handler` 零命中；`BookingService` 接口签名里只出现 domain 类型
- pytest 全绿（`make test`）——真实 HTTP + 直连双库，这一步唯一的回归网
- `docker compose up` 起得来，行为与改动前完全一致
- `go test -race ./...` 过
- **怎么回滚**：单个 commit revert；不改数据库、不改 OpenAPI 契约

## 注意事项

**纯翻译搬运，不掺任何「顺便改一下」。** [D-23](./D-23-concurrent-release-double-refund.md)（并发取消重复归还）、[D-24](./D-24-booking-idempotency-key.md)（无幂等保护）都在这批代码附近，**不要在这一条里碰它们**——它们要的是能变红的复现测试，而不是重构时顺手改掉。

`api.gen.go` 是生成物，**不能手改**（`CLAUDE.md` § 1）。handler 里 domain → API 响应类型的翻译写在 handler 自己的文件里。

## 学到什么

第二次做同一件事时，**值得记的不是怎么做，而是哪些地方和第一次不一样**。flight 侧的 `FlightRow` 只被读，booking 的 `BookingRow` 有写路径（状态迁移），所以翻译是双向的——单向翻译能糊弄过去的地方（比如枚举值拼错），双向会立刻暴露。

**双向翻译是单向翻译的自检**：写进去再读出来必须相等，这条性质本身就是一条测试。
