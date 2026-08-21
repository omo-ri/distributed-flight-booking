---
id: D-25
title: 整条调用链没有任何超时，下游卡死会拖垮上游
severity: critical
status: todo
phase: 0
blocks: [D-21]
refs:
  - booking-service/internal/grpcclient/flight.go
  - booking-service/cmd/main.go
  - flight-service/internal/repository/flight.go
---

# D-25 整条调用链没有任何超时，下游卡死会拖垮上游

**位置**：`booking-service/internal/grpcclient/flight.go`（全文件无 `context.WithTimeout`）、`booking-service/cmd/main.go:90-104`（无超时中间件）

## 现象 / 触发场景

`grep -rn 'WithTimeout\|WithDeadline' booking-service/ flight-service/` **无任何匹配**。整条链路上没有一处设置过 deadline：

| 层 | 超时 | 后果 |
|---|---|---|
| HTTP 入口（Echo 中间件链） | 无 | 请求 context 没有 deadline |
| gRPC 客户端调用 | 无 | 直接复用上面那个无 deadline 的 context |
| `SELECT ... FOR UPDATE` 等锁 | 无（`lock_timeout` 未设置） | PostgreSQL 默认无限等待 |
| Redis 操作 | 无 | Redis 变慢时每个请求都被拖住 |

**第一个后果是一个死代码分支**。重试表里写着：

```go
var retryableCodes = map[codes.Code]bool{
    codes.Unavailable:      true,
    codes.DeadlineExceeded: true,      // ← grpcclient/flight.go:26
}
```

`DEADLINE_EXCEEDED` 只有在设置了 deadline 时才会产生。**没人设过，所以这个分支永远不会被触发**——重试机制实际上只对 `UNAVAILABLE` 生效，看起来覆盖了两种情况，实际只覆盖一种。

**第二个后果是故障放大，这才是真正严重的部分**。当 flight-service 变慢（比如撞上 `docs/design/system-design.md` § 5.1 算出的单航班行锁上限，约 300–1000 单/s）：

```
flight-service 的 ReserveSeats 在 FOR UPDATE 上排队，越排越久
  ↓ booking-service 的调用永远不放弃（无 deadline）
  ↓ goroutine 和 HTTP 连接持续堆积，内存上涨
  ↓ pgxpool 的连接被耗尽（未配置，默认 MaxConns = max(4, NumCPU) = 12）
  ↓ 连「查订单」这种根本不碰 flight-service 的请求也开始等连接
  ↓ 熔断器记不到任何失败 —— 调用根本没返回，走不到 RecordFailure()
  ↓ booking-service 整体无响应
```

**注意倒数第二步**：熔断器是专门用来打断这条链的，但**它只能对"返回了错误的调用"计数**。调用永远挂着，熔断器就永远处于 CLOSED，保护机制完全失效。

**这决定了故障的形状**：有超时的话，撞上库存上限表现为"一部分用户下单失败"（可接受，本来就没座位了）；没有超时的话，表现为"整个网站挂掉"（不可接受）。**真正把系统拖垮的不是锁，是缺超时。**

## 根因

超时是**调用方**对"我最多愿意等多久"的声明，而这个声明从来没有被写下来。Go 的 `context` 机制让"不设超时"成为零成本的默认选项——`c.Request().Context()` 传下去就能编译通过、能跑通、测试全绿，只有在下游真正卡住时才显形。

## 修法

自上而下三层，缺一层都会漏：

1. **HTTP 入口**：加请求超时中间件（`middleware.TimeoutWithConfig`），作为整体预算的上界
2. **每个 gRPC 调用**：`ctx, cancel := context.WithTimeout(ctx, cfg.FlightRPCTimeout)`，超时值走环境变量（照 `CB_*` 的形式，见 `CLAUDE.md` § 4 的配置分层）
3. **数据库**：给 flight-service 的事务设 `lock_timeout`（`SET LOCAL lock_timeout = '...'`），让锁等待有上界而不是无限排队

**超时值必须由延迟预算倒推，不能拍脑袋**：

```
R2 软要求：尖峰 p95 < 500ms
一次 CreateBooking = GetFlight + ReserveSeats 两次 gRPC
当前重试策略 = 1 次初始 + 最多 3 次重试，退避 100+200+400 = 700ms

若单次 RPC 超时设 150ms：
  最坏 = 4 × 150ms + 700ms 退避 = 1300ms  ← 已经是 R2 预算的 2.6 倍
```

**所以这条改动会连带逼出一个结论：3 次重试太多。** 在 500ms 预算里，一次超时 + 一次重试就已经用掉大半。**重试次数是被延迟预算决定的，不是独立选的。**

另外，超时值设短了会在尖峰的正常锁等待中误杀请求。**这个值必须由压测出来的 p99 决定**，而 2026-05-29 那次基线被脚本里的 `sleep` 限住，只测到了 k6 自己的天花板。[T-06](./T-06-k6-three-scenarios.md) 已把压测改成开环阶梯，取值等 [T-07](./T-07-local-capacity-baseline.md) 的报告。

**T-06 的写路径实测顺带证实了本条的前提**：单航班写压过拐点后，系统**完全不丢负载**——2xx 始终 100%，延迟一路涨到 2 秒，吞吐甚至倒退。没有超时就没有快速失败，只有无限排队。

## 依赖关系

**本条阻塞 [D-21](./D-21-half-open-no-probe-limit.md)**：D-21 要让 HALF_OPEN 只放行 1 个探测请求，但如果那个探测**永远不返回**，熔断器会卡在 HALF_OPEN 且再也不放行任何请求——比 OPEN 还糟，因为 OPEN 至少会到期。**必须先有超时，HALF_OPEN 限流才是安全的。**

## 验收标准

- `docker compose pause flight-service`（**pause 而不是 stop**——要模拟"卡住"而不是"拒绝连接"）后调 `POST /bookings`：在超时值内返回错误，**不是无限等待**
- 同一场景下，`GET /bookings/{id}`（不经过 flight-service）**仍然正常响应**——故障没有扩散
- 同一场景下熔断器能正常打开（依赖 [D-01](./D-01-circuit-breaker-error-classification.md) 的状态指标验证）
- `codes.DeadlineExceeded` 的重试分支有测试覆盖，且能在上述场景中实际命中
- 重试次数经过延迟预算重新核算，`grpcclient` 的 `maxRetries` 与超时值一起作为配置项，并在 `README.md` 的环境变量表中登记（`CLAUDE.md` § 4 要求）

## 学到什么

**没有超时的调用不是"耐心"，是"没有放弃条件"**——而分布式系统里最危险的状态不是失败，是永远不失败也不成功。

三条可以固定下来的判据：

1. **每一次跨进程调用都必须有 deadline。** 没有 deadline 的调用会把下游的故障原样传染给上游，而且传染是不可逆的（连接已经堆积了，下游恢复也救不回来）。
2. **熔断器保护不了"卡住"，只保护得了"报错"。** 熔断、重试、限流这些机制全部建立在"调用会返回"这个前提上，而这个前提由超时提供。**超时是所有韧性机制的地基**，不是其中一项。
3. **超时、重试次数、延迟预算是一组联立方程，不能分别决定。** 这个仓库里重试次数（3 次）和退避（100/200/400ms）都是先定下来的，而它们的总和已经超出了延迟预算——因为当时没有超时，这个矛盾从来没有暴露过。
