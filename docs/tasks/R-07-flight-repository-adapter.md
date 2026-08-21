---
id: R-07
title: flight-service repository 改成出站适配器
severity: major
status: todo
phase: foundation
blocks: [R-08, R-09]
refs:
  - flight-service/internal/repository/flight.go
  - flight-service/internal/service/flight.go
---

# R-07 flight-service repository 改成出站适配器

**位置**：`flight-service/internal/repository/flight.go`（`FlightRow` `:13`、`SeatReservationRow` `:27`、五个查询方法）；跟随修改 `service/flight.go`、`handler/flight.go`。

## 为什么需要

依赖方向要指向内层（[R-06](./R-06-flight-domain-package.md) 已经把内层建好了）：

```
handler ──▶ service ──▶ domain ◀── repository
```

现在箭头是反的——`repository` 定义类型，service 被迫接受，handler 跟着一起知道数据库长什么样。这一步把箭头掰过来。

**适配器负责翻译，翻译不外泄。** repository 从此对外只说 `domain` 的语言：行结构翻成 `domain.Flight`，`pgx.ErrNoRows` 翻成 `domain.ErrFlightNotFound`。service 从此不知道 `pgx` 存在。

## 做什么

**1. 五个方法的返回类型换成 domain 类型**（`SearchFlights` `:44`、`GetFlightByID` `:61`、`ReserveSeats` `:81`、`ReleaseReservation` `:137`、内部的 `scanFlights` `:175`）。

行结构体保留但**降级成包内私有**——它是扫描 SQL 结果的中转，不再是对外契约。

**2. 驱动错误在这里翻译，不再往上漏**：`repository.ErrNotFound` / `repository.ErrInsufficientSeats` 换成 domain 的对应物。座位不足要带上实际可用数，填进 `domain.InsufficientSeatsError`。

**3. service 与 handler 机械跟随**——只换签名类型，**不改任何逻辑**。`handler/flight.go:96-128` 的 `flightRowToProto` / `reservationRowToProto` 改成从 domain 类型翻，函数名跟着改。

**本条不动错误匹配**：`handler/flight.go:48,:67,:86` 那三处越级匹配继续存在（它们会自然变成匹配 domain 错误），彻底整改归 [R-08](./R-08-flight-handler-adapter.md)。**不动缓存**：`s.cache == nil` 那一串继续存在，归 [R-09](./R-09-flight-service-ports.md)。

## 验收标准

- **怎么验证它生效了**：`grep -rn "repository\." --include=*.go internal/service internal/handler` 只剩构造函数与错误名，`FlightRow` / `SeatReservationRow` 零命中
- service 的对外接口（`service/flight.go:12-17`）签名里只出现 `domain` 类型
- pytest 全绿（`make test`）——它走真实 gRPC + 直连数据库，对内部结构一无所知，是这一步唯一的回归网
- `docker compose up` 起得来，行为与改动前**完全一致**
- `go test -race ./...` 过，现有 `handler/flight_test.go` 的断言全部保留
- **怎么回滚**：单个 commit revert；本条不改数据库、不改 API 契约

## 注意事项

**这次改动必须是纯翻译搬运。** 不顺手修 [D-20](./D-20-internal-error-disclosure.md)、不顺手改 nil 判断、不调整任何错误码。一旦掺进「顺便改一下」，pytest 全绿就不再能证明什么。

如果撞到了纸面设计没想到的东西（比如某个字段在 domain 里找不到合适的位置），**先记下来再决定**——flight 是试验田，撞到问题改设计的成本很低，但前提是撞到时你知道自己撞到了。

## 学到什么

**「返回持久化类型」是最容易被接受的耦合**，因为它不需要写任何额外代码——数据库已经给了你一个结构体，转手就是。省下的是一次翻译，付出的是让业务层永久知道表结构。

翻译代码看起来是纯粹的样板（把 A 的字段抄进 B），但它买到的是**变更的边界**：加一列不再需要动三层。样板的价值不在它写了什么，在它拦住了什么。
