---
id: D-19
title: gRPC 错误码到 HTTP 状态码的映射不完整
severity: major
status: todo
phase: 0
blocks: []
refs:
  - booking-service/internal/handler/booking.go
  - booking-service/internal/service/booking.go
---

# D-19 gRPC 错误码到 HTTP 状态码的映射不完整

**位置**：`booking-service/internal/handler/booking.go:53-59`、`:78-84`、`:122-128`、`:184-190`

## 现象 / 触发场景

每个 handler 的错误处理都是同一个形状：识别两三个已知错误，剩下的**全部落到 500**。

```go
if errors.Is(err, service.ErrFlightNotFound) { ... 404 }
if errors.Is(err, service.ErrInsufficientSeats) { ... 409 }
if err != nil {
    if isCircuitOpen(c, err) { return nil }        // 503
    return c.JSON(http.StatusInternalServerError, ...)   // ← 其余全在这里
}
```

于是这些 gRPC code 全部变成 HTTP 500：

| gRPC code | 触发场景 | 当前 | 应该是 |
|---|---|---|---|
| `UNAVAILABLE` | flight-service 挂了 / 正在重启 | **500** | 503 |
| `DEADLINE_EXCEEDED` | 下游超时 | **500** | 504 |
| `UNAUTHENTICATED` | `AUTH_API_KEY` 两边不一致 | **500** | 502（下游拒绝了我们） |
| `INVALID_ARGUMENT` | 参数没通过 flight-service 的校验 | **500** | **400** |

**两个具体后果**：

1. **污染错误率 SLI**。`INVALID_ARGUMENT` 本质是客户端的错，却被计成 `error_type="server_error"`（`booking-service/internal/metrics/metrics.go` 的 Middleware 按状态码分类）。这和 [D-02](./D-02-error-rate-sli-server-errors-only.md) 是同一个方向的问题——D-02 修完之后错误率只算 5xx，而这里会让客户端的错继续留在 5xx 里，**D-02 修了也不干净**。
2. **误导客户端和排障方向**。500 的语义是"服务器有 bug"，客户端不会重试；503/504 是"稍后再来"。下游只是重启了一下，客户端却得到一个"永久失败"的信号。而 `UNAUTHENTICATED` 被伪装成 500，排障的人会去翻 booking-service 的代码，而真正的问题是一个环境变量配错了。

## 根因

错误映射是"识别已知的、其余兜底"的写法，而不是"穷举 code、显式映射"。兜底分支把**四类完全不同的失败**压成了同一个状态码，信息在这里被丢弃了。

对比 flight-service 侧：那边的映射是完整的（`flight-service/internal/handler/flight.go` 把每个内部错误都显式映射到一个 code）。**丢信息发生在第二次映射，不是第一次。**

## 修法

抽一个 `grpcCodeToHTTP(err error) int` 函数，显式覆盖全部会出现的 code，四处 handler 共用。默认分支保留 500，但要能通过测试确认默认分支不会被上表那四种 code 命中。

同时把 `service` 层的错误包装保留 gRPC status——当前 `service/booking.go:110` 等处用 `fmt.Errorf("get flight: %w", err)` 包装，`status.FromError` 对 `%w` 包装过的错误仍然可用（handler 里已经这么做了），但要确认每一条路径都没有丢掉 status。

## 验收标准

- `docker compose stop flight-service` 后调 `GET /flights` → **503**（当前 500）
- 把 booking-service 的 `AUTH_API_KEY` 改成错的 → **502**（当前 500）
- 座位不足仍然是 **409**、航班不存在仍然是 **404**、熔断打开仍然是 **503**（回归）
- 表格驱动的单元测试覆盖每一个映射条目，包括默认分支
- 上述场景下 `http_request_errors_total{error_type="server_error"}` 只在真正的 5xx 时增长

## 学到什么

**错误码是给机器看的路标，不是给人看的描述。** 判断一个映射对不对，只问一句：客户端拿到这个码，接下来该做什么？404 → 放弃；409 → 换个参数；503 → 稍后重试；500 → 报告 bug。四个不同的动作被压成一个码，就等于什么都没告诉对方。

同时这条也说明**SLI 的正确性依赖整条链路**：D-02 修的是聚合口径，D-19 修的是数据源头，只修一头都不够。
