---
id: R-06
title: flight-service 新增 domain 包与领域错误
severity: major
status: todo
phase: foundation
blocks: [R-07, R-08, R-09]
refs:
  - flight-service/internal/repository/flight.go
  - flight-service/internal/handler/flight.go
---

# R-06 flight-service 新增 domain 包与领域错误

**位置**：新增 `flight-service/internal/domain/`。现有类型在 `repository/flight.go:13-34`（`FlightRow` / `SeatReservationRow`）。

设计推演见 [`design/code-structure.md`](../design/code-structure.md) § 一、§ 三。

## 为什么需要

**领域模型现在住在 `repository` 里。** service 的对外契约（`service/flight.go:12-17`）返回 `repository.FlightRow`，handler 也 `import repository`——数据库加一列，涟漪一路推到 gRPC 响应。

更根本的是：想理解一条业务规则，必须同时理解数据库的表结构。判断一个类型该不该进 domain 有一条判据——**换掉 Postgres、换掉 gRPC、换掉 Redis，它还在吗？**

这是第 2 步（flight 整体倒置）的第一刀，**纯新增、没有调用方**，所以它必然是绿的。

## 做什么

新增三个文件：

```
internal/domain/
├── flight.go     Flight 实体 + FlightStatus 枚举
├── reservation.go SeatReservation 实体 + ReservationStatus 枚举
└── errors.go     领域错误
```

**1. 实体是纯数据，没有 import（除标准库）、没有 tag、没有方法调用外部。**

状态用具名类型而不是裸 `string`——现在 `handler/flight.go:130-147` 的 `flightStatusToProto` / `reservationStatusToProto` 是在拿字符串字面量做 switch，枚举收进 domain 之后，翻译表有了单一落点。

**2. 领域错误按一条判据分形式**——*这个错误的接收方，除了「哪一类错」之外还需要知道别的吗？*

不需要 → sentinel：

```go
var (
    ErrFlightNotFound      = errors.New("flight not found")
    ErrReservationNotFound = errors.New("active reservation not found")
)
```

需要 → 错误类型：

```go
type InsufficientSeatsError struct {
    Requested int32
    Available int32
}
```

座位不足是这个系统里最高频的业务拒绝，而「还剩几个」既是调用方想知道的，也是排障时想在汇总行里看到的（`reason=insufficient_seats` 后面跟一个 `available=1`）。只用 sentinel 的话这个数字只能靠 `fmt.Errorf` 拼进字符串——**日志字段挂不上，API 响应也拼不出**。

不要走极端：全用 sentinel 会在最需要细节的地方失效；全用类型会让 `ErrFlightNotFound` 这种零数据的错误也背上一个 struct 和一个 `Error()` 方法。

## 验收标准

- **怎么验证它生效了**：一条守卫测试——用 `go/parser` 或 `go list -deps` 断言 `internal/domain` 的依赖里**只有标准库**。这条断言是 domain 包唯一的硬约束，靠人自觉守不住
- `InsufficientSeatsError` 的 `Error()` 输出含请求数与可用数；`errors.As` 能取回结构化字段
- `go build ./...` 与 `go test -race ./...` 过
- 本条**不改任何现有文件**——diff 里只有新增
- **怎么回滚**：删目录

## 注意事项

**先不要动 `repository.FlightRow`。** 类型迁移是 [R-07](./R-07-flight-repository-adapter.md) 的事，本条只把新的家盖好。两件事分开的理由是：盖房子必然绿，搬家可能撞到东西，混在一起就分不清是哪一步出的问题。

## 学到什么

**「模型和 service 混在一起」这个感觉其实说轻了——模型压根没有自己的家。** service 只是转手 repository 定义的类型，所以「把模型从 service 里拆出来」这个动作找不到起点：它不在那儿。

先建立一个**不依赖任何东西**的层，后面每一步才有确定的方向可指。依赖倒置不是从「加接口」开始的，是从「有一个所有人都能依赖、而它谁也不依赖的东西」开始的。
