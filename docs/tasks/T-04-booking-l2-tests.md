---
id: T-04
title: booking-service L2：本地 repository + pb 契约桩驱动的跨边界测试
severity: critical
status: todo
phase: foundation
blocks: [T-05]
refs:
  - booking-service/internal/repository/booking.go
  - booking-service/internal/service/booking.go
  - booking-service/internal/handler/booking.go
  - booking-service/internal/grpcclient/flight.go
---

# T-04 booking-service L2：本地 repository + pb 契约桩驱动的跨边界测试

**前置**：[T-02](./T-02-testcontainers-harness.md)

## 为什么需要

booking-service 上的几条 critical 缺陷全部在**服务边界上**，它们测的是"booking 对下游行为的反应"。这类测试需要的不是真实对端，是**可控对端**——理由见 [`conventions/testing.md`](../conventions/testing.md) § 4。

而"在 booking 的测试里起真实 flight-service"在 Go 里编译不过（`internal` 规则跨模块生效）。可行的做法是用 `pb.RegisterFlightServiceServer` 注册测试桩——`pb` 是公开的生成包。

## 做什么

**本地 repository（真实 PG，`booking_db`）**：订单的写入、查询、状态迁移（`CONFIRMED → CANCELLED`，已取消的不能再取消）。

**契约桩驱动的跨边界测试**：在测试进程内起一个 gRPC server，桩实现按用例返回指定的 code / 延迟 / 序列。覆盖：

| 用例 | 桩的行为 | 断言 |
|---|---|---|
| 状态码映射（[D-19](./D-19-grpc-http-status-mapping.md)） | 依次返回 `NOT_FOUND` / `RESOURCE_EXHAUSTED` / `UNAVAILABLE` / `DEADLINE_EXCEEDED` / `UNAUTHENTICATED` / `INVALID_ARGUMENT` | HTTP 状态码符合 [`design/system-design.md`](../design/system-design.md) § 3.3 的映射表 |
| 无超时（[D-25](./D-25-no-timeouts-anywhere.md)） | 接受连接但**永远不回包** | 调用应在预算内返回，而非无限等待 |
| 熔断分类（[D-01](./D-01-circuit-breaker-error-classification.md)） | 连续返回业务错误（`NOT_FOUND`） | 熔断器保持 CLOSED |
| HALF_OPEN 限流（[D-21](./D-21-half-open-no-probe-limit.md)） | 按脚本控制成功/失败序列 | 半开时只放行限定数量的探测 |

## 验收标准

- **D-19 的映射测试能稳定变红**——`UNAVAILABLE` / `INVALID_ARGUMENT` 当前全部落到 HTTP 500（`handler/booking.go:122-128` 及 `:53-59`、`:78-84`、`:184-190` 四处同构）
- 映射表的测试是**表驱动**的，加一个 code 只需加一行
- 修复不属于本条，见 [T-03](./T-03-flight-l2-tests.md) 的同一条说明

## 注意事项

**映射逻辑本身应该先下沉到 L1。** 它是一张纯查表，按准入规则（能在上一档证明的不许放到下一档）必须有 L1 表格测试；L2 只负责验证"真的有 gRPC 错误穿过边界时，这张表确实被用上了"。两者不重复。

**桩会和真实实现漂移**——这是选桩买下的代价。这个漏洞由 L3 的契约一致性测试补，不要试图在 L2 里解决。

## 学到什么

**故障注入的可控性，比对端的真实性更重要。** "永远不回包"这种行为，真实服务不会主动提供，硬要它提供就得往生产代码里塞测试专用分支。桩把这件事挪到了测试侧——**测试的复杂度应该由测试承担，而不是渗回实现里。**
