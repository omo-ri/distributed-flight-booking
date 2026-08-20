---
id: D-21
title: 熔断器 HALF_OPEN 状态不限流，恢复瞬间会放行全部流量
severity: major
status: todo
phase: 0
blocks: []
refs:
  - booking-service/internal/circuitbreaker/breaker.go
---

# D-21 熔断器 HALF_OPEN 状态不限流，恢复瞬间会放行全部流量

**位置**：`booking-service/internal/circuitbreaker/breaker.go` 的 `Allow()`

## 现象 / 触发场景

```go
case Open:
    if time.Since(cb.lastFailure) > cb.timeout {
        cb.setState(HalfOpen)
        return nil // allow one probe request
    }
    return ErrCircuitOpen
case HalfOpen:
    return nil // allow the probe request
```

注释写的是"放行**一个**探测请求"，但代码里 `HalfOpen` 分支对**每一个**调用都返回 `nil`——没有任何计数器限制放行数量。

**触发场景**：flight-service 挂了 30 秒，熔断器 OPEN。这 30 秒里 booking-service 持续收到请求，全部快速失败（这部分工作正常）。第 30 秒，第一个请求把状态推到 HALF_OPEN——**此后所有并发到达的请求全部被放行**，一起涌向刚刚重启、连接池还没热、缓存还是空的 flight-service。

按 `docs/design/system-design.md` § 1.3 的尖峰规模（2000 QPS），这意味着**瞬间数千个请求同时打到一个刚起来的服务上**。大概率立刻再次压垮它 → 熔断器重新 OPEN → 30 秒后重复一次。系统进入"OPEN 30 秒 / 惊群一次"的循环，**永远恢复不了**。

## 根因

HALF_OPEN 这个状态存在的**唯一理由**就是限流探测——它要回答的问题是"下游好了吗"，而回答这个问题只需要一个请求。当前实现让这个状态只起到了"延迟 30 秒"的作用，等价于"OPEN 到期后直接回到 CLOSED"。

也就是说：**三态机在行为上退化成了两态机**，而两态机恰恰是 HALF_OPEN 被发明出来要修正的东西。

## 修法

给 `CircuitBreaker` 加一个 `halfOpenProbes int` 计数器，`Allow()` 在 HALF_OPEN 状态下只放行前 N 个（N 从 1 开始），其余仍然返回 `ErrCircuitOpen`。`RecordSuccess` / `RecordFailure` 在离开 HALF_OPEN 时重置它。

**必须一起处理的边界**：探测请求如果**一直不返回**，熔断器会卡在 HALF_OPEN 且再也不放行任何请求——比 OPEN 还糟，因为 OPEN 至少会到期。这需要给探测本身设超时，而全链路当前**没有任何 `context.WithTimeout`**（`booking-service/internal/grpcclient/flight.go` 里一处都没有）。

所以本条有一个隐含前置：**先有超时，HALF_OPEN 限流才是安全的。**（超时本身尚未登记为独立任务，见 `docs/design/system-design.md` § 2.3 🔧。）

建议和 [D-01](./D-01-circuit-breaker-error-classification.md) 一起做——同一个包、同一次改动、同一套单元测试，而且 D-01 要加的熔断器指标正好能验证本条的效果。

## 验收标准

- 单元测试：熔断器处于 OPEN 且已超时后，**并发**发起 100 次 `Allow()`，只有 1 次返回 `nil`，其余返回 `ErrCircuitOpen`
- 单元测试：探测成功 → 状态回到 CLOSED，后续调用全部放行；探测失败 → 回到 OPEN，计数器已重置
- 集成场景：`docker compose stop flight-service` → 等熔断打开 → `start` → 观察 flight-service 的 `http_requests_total` 在恢复瞬间**不出现尖峰**
- 依赖 D-01 的 `circuit_breaker_rejected_total` 指标：能看到 HALF_OPEN 期间确实有请求被拒绝

## 学到什么

**注释描述的是意图，代码描述的是行为，两者不一致时线上跑的是后者。** 这里的注释（"allow one probe request"）准确地写出了正确设计，但代码没有实现它——而且因为只有在"下游从故障中恢复"这个罕见时刻才会暴露，单元测试和日常使用都碰不到。

更一般的教训：**韧性机制的缺陷只在故障时显形，而那恰恰是最不能出错的时刻。** 这类代码不能靠"平时没出问题"来验证，只能靠显式注入故障来验证——这也是 `roadmap.md` 阶段 5 混沌工程的意义所在。
