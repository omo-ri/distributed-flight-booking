---
id: R-01
title: logctx 语义化封装与按字段的写入语义
severity: major
status: todo
phase: foundation
blocks: [R-02, R-03]
refs:
  - booking-service/internal/logctx/logctx.go
  - flight-service/internal/logctx/logctx.go
---

# R-01 logctx 语义化封装与按字段的写入语义

**位置**：`booking-service/internal/logctx/logctx.go:83-124`、`flight-service/internal/logctx/logctx.go:75-114`，以及五处调用点。

设计推演见 [`design/code-structure.md`](../design/code-structure.md) § 四。

## 为什么需要

**两个独立的坑，同一个根因：写入语义留给了调用方。**

### 坑一：「只写对一半」

表达一次降级现在要写两行，而这个惯用法在仓库里抄了五遍：

| 位置 | 降级原因 |
|---|---|
| `booking-service/internal/service/booking.go:171-172` | `release_failed` |
| `flight-service/internal/service/flight.go:127-128` | `search_cache_invalidate_failed` |
| `flight-service/internal/cache/redis.go:78-79` | `cache_set_failed` |
| `flight-service/internal/cache/redis.go:109-110` | `cache_set_failed` |
| `booking-service/internal/grpcclient/flight.go:116-117` | `retry_recovered` |

漏掉 `Escalate` 那一行，降级就静默停留在 `Info`——在 `LOG_LEVEL=warn` 的压测里**整条记录消失**，而代码看起来完全正常。编译器抓不到，评审很容易放过，出错概率随抄写次数线性增长。

### 坑二：`degraded` 被覆盖，原因丢一半

`set()`（booking `:100-108` / flight 同构）是同名 key 覆盖。一次 `ReserveSeats` 里可能先后发生：

```go
logctx.Add(ctx, KeyDegraded, "cache_set_failed")                // redis.go:79
logctx.Add(ctx, KeyDegraded, "search_cache_invalidate_failed")  // flight.go:128
// 汇总行里只剩后一个
```

`Escalate` 只升不降，所以**级别是对的**（还是 Warn），但**原因丢了一半**。不报错、不改级别，只是让排障时看到的因果链是残缺的。

**覆盖本身不是错的**——`retries` 要的就是最终次数、`cb` 要的就是最终状态。错的是用一套写入语义套所有字段。

## 做什么

两个模块各改一份（跨模块不共享代码，`CLAUDE.md` § 4）。

**1. 三个语义函数，`Escalate` 转私有：**

```go
func Degraded(ctx context.Context, what string)  // 累加去重 + 内部抬 Warn
func Reject(ctx context.Context, reason string)  // 首写胜出，级别恒为 Info
func Fail(ctx context.Context, err error)        // 只记 error，级别交给入口按状态码判
```

`Add` 保持公开——「记一个事实」（`logctx.Add(ctx, KeyBookingID, id)`）是正当用法。转私有的只有 `Escalate`：抬级别从此不是调用方的责任，「忘了抬」在结构上不再可能发生。

**2. 写入语义按字段定，不按包定：**

| 语义 | 字段 | 理由 |
|---|---|---|
| 覆盖（默认） | `retries`、`cb`、`error`、`cache`、业务字段 | 要的是最终值 |
| 累加去重 | `degraded` | 要的是完整清单 |
| 首写胜出 | `reason` | 第一个拒绝原因是真原因，后面的都是它的后果 |

累加输出成逗号串而不是 JSON 数组：`"degraded":"release_failed,cache_set_failed"`。理由是 Loki 侧 `|= "release_failed"` 直接命中，数组还得多绕一层。

**3. 五处调用点各塌成一行**，`flight-service/internal/auth/interceptor.go:25,:31` 与 `booking-service/internal/handler/booking.go:22,:32` 的拒绝改走 `Reject`。

## 验收标准

- 单测：连续两次 `Degraded(ctx, "a")` / `Degraded(ctx, "b")` 得到 `"a,b"`；同一个原因写两次只出现一次
- 单测：连续两次 `Reject` 只保留第一个 `reason`
- 单测：`Degraded` 之后 `Collect` 返回的级别是 `Warn`（不需要调用方做任何额外的事）
- `grep -rn "Escalate" --include=*.go` 在 `internal/logctx` 之外零命中
- `go test -race ./...` 两个模块都过；现有 `logctx_test.go` 的断言全部保留
- **怎么知道它出问题了**：汇总行里出现 `degraded` 但级别是 `Info`，说明封装被绕过了
- **怎么回滚**：三个函数是纯增量，`Escalate` 改回公开即可退回原状

## 注意事项

`degraded` 累加是有上界的——取值来自代码里的字面量常量集合，不会因流量增长而膨胀。真出现十几个拼在一起，那是系统在同一条请求上降级了十几次，那条日志本身就是要看的东西。

## 学到什么

**累加语义不可能留给调用点。**「先读出来、拼上、去重、再写回」没人会每次写对——这条反过来证明了封装不是风格偏好，而是正确性的载体。

一个「抄五遍都能抄对」的两行惯用法，和一个抄一遍就没机会写错的函数，差别不在优雅，在于**错误是否可能发生**。
