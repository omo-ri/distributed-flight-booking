---
id: R-10
title: booking-service 新增 domain 包，sentinel 从业务文件迁出
severity: major
status: todo
phase: foundation
blocks: [R-11, R-12, R-13]
refs:
  - booking-service/internal/service/booking.go
  - booking-service/internal/repository/booking.go
---

# R-10 booking-service 新增 domain 包，sentinel 从业务文件迁出

**位置**：新增 `booking-service/internal/domain/`。现有 sentinel 在 `service/booking.go:18-23`，`FlightInfo` 在 `:25-37`。

**第 3 步的第一刀。设计已经被 flight 侧（[R-06](./R-06-flight-domain-package.md) ~ [R-09](./R-09-flight-service-ports.md)）验证过一遍，这一步是照搬。**

## 为什么需要

booking 的错误处理是全项目最干净的地方（service 定义 sentinel、handler 用 `errors.Is` 映射），**位置对但住错了地方**：sentinel 埋在 `booking.go:18-23`，夹在 import 和业务代码中间。错误是**跨层契约**，散在实现里就没法一眼看全。

同时 `service/booking.go:39-46` 的对外契约返回 `repository.BookingRow`，`handler/booking.go:14` 因此必须 `import repository`——和 flight 侧同一个病。

## 做什么

```
internal/domain/
├── booking.go   Booking 实体 + BookingStatus 枚举
├── flight.go    Flight 实体（现在的 service.FlightInfo）
└── errors.go    领域错误
```

**1. `service.FlightInfo`（`:25-37`）迁成 `domain.Flight`。** 它现在已经是一个不依赖 pb 的纯结构体——方向本来就对，只是住在 service 里。搬家顺带统一命名：它描述的是航班，不是「航班信息」。

**2. `repository.BookingRow` 的领域形态迁成 `domain.Booking`**，状态从裸 `string` 换成 `BookingStatus` 具名类型。

**3. 四个 sentinel（`:18-23`）迁进 `domain/errors.go`**，`ErrInsufficientSeats` 升级成 `InsufficientSeatsError` 类型，带 `Requested` / `Available`。

理由和 flight 侧同一条：座位不足是这个系统最高频的业务拒绝，「还剩几个」既是调用方想知道的，也是排障时想在汇总行里看到的。sentinel 只能把这个数字拼进字符串，**日志字段挂不上，API 响应也拼不出**。

**4. 本条不改任何调用点。** 用 `var ErrNotFound = domain.ErrNotFound` 这类别名让旧引用继续编译，别名在 [R-13](./R-13-booking-service-ports.md) 里删干净——expand 在前，contract 在后。

## 验收标准

- **怎么验证它生效了**：守卫测试断言 `internal/domain` 的依赖里只有标准库（与 flight 侧同一条断言）
- `errors.As` 能从 `InsufficientSeatsError` 取回请求数与可用数
- `go build ./...` + `go test -race ./...` 过，**现有测试一条都不用改**（别名兜住了）
- pytest 全绿
- **怎么回滚**：删目录 + 删别名

## 注意事项

**照搬不等于不看。** 设计已经验证过，但 booking 比 flight 多一层（`grpcclient`），domain 里要不要给「从下游取回的航班」和「本地持久化的订单」不同的建模粒度，是这一步唯一需要现场判断的事。撞到就记下来。

## 学到什么

**「位置对但埋错文件」比「位置错」更难发现**——`errors.Is` 用得很规范，映射表也在 handler，静态检查和评审都挑不出毛病。只有当你想回答「这一层一共会返回哪些错误」时才会发现：得读完 211 行才知道。

**跨层契约要有一个能被单独打开的文件**，理由和 `ports.go` 一样：它回答的是「我手上有什么」，而这个问题不该靠通读实现来回答。
