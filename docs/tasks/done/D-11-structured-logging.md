---
id: D-11
title: 日志不可用于排障
severity: major
status: done
phase: 1
blocks: []
refs:
  - booking-service/cmd/main.go
  - booking-service/internal/logctx/logctx.go
  - flight-service/internal/logging/interceptor.go
---

# D-11 日志不可用于排障

三个独立问题：

1. **格式不一致**：booking-service 是 slog JSON，flight-service 是标准库文本（`[CACHE] HIT flight:xxx`）。一套解析规则覆盖不了两个服务。
2. **无关联**：booking-service 有 request_id，但没有通过 gRPC metadata 传给 flight-service。一次跨服务调用在两边的日志里无法串联。
3. **关不掉**：`flight-service/internal/auth/interceptor.go` 每个请求成功都打一行 `[AUTH] OK`；cache 每次 HIT/MISS/SET/DEL 都打日志。flight-service 用标准库 `log.Printf`，没有级别概念，压测时想关也关不掉。实测代价：一次读路径压测 385 万请求 × 每请求至少 2 行 = 770 万行、几个 G。

**结果**：出了故障只能靠 `docker compose logs` 肉眼翻，这不是运维手段。

## 做什么

| # | 动作 | 状态 |
|---|---|---|
| 1 | booking-service `LOG_LEVEL` 环境变量 + `parseLogLevel`，compose 传 `${LOG_LEVEL:-info}` | ✅ |
| 2 | flight-service `log.Printf` → `slog` + JSON + `LOG_LEVEL`，与 booking 侧同构 | ✅ |
| 3 | trace_id 跨 gRPC metadata 贯通 | ✅ |

第 3 点做的时候顺带重新设计了整套输出口径：**一次请求一条汇总行**，下层不持有 logger、把字段挂进 `logctx`。规则见 [`CLAUDE.md`](../../../CLAUDE.md) § 4，字段白名单与被否掉的方案见 [`conventions/engineering.md`](../../conventions/engineering.md) § 5。

## 验收标准

- [x] 两个服务的输出全是 JSON，一套解析规则覆盖（含 go-redis 的库内日志，经 `redis.SetLogger` 接进 slog）
- [x] 一次跨服务调用在两侧日志里能用同一个 `trace_id` join
- [x] `LOG_LEVEL=warn` 时两个服务的正常请求路径**一行不落盘**
- [x] 业务拒绝是 `Info`，已自动处理的降级是 `Warn`，内部错误是 `Error`
- [x] 一次 `POST /bookings` 的日志行数从 11 行降到 3 行

## 实测记录（2026-08-21，本机 compose）

跨服务串联，同一个 `trace_id` 串起 booking 的 HTTP 与 flight 的两次 RPC：

```json
{"level":"INFO","msg":"request","trace_id":"pViMAFgf...","route":"/bookings","status":201,"latency_ms":23,"user_id":"1111...","flight_id":"a0ee...","seat_count":2,"booking_id":"467720b8-..."}
{"level":"INFO","msg":"rpc","trace_id":"pViMAFgf...","route":"/flight.FlightService/GetFlight","code":"OK","latency_ms":1,"cache":"miss"}
{"level":"INFO","msg":"rpc","trace_id":"pViMAFgf...","route":"/flight.FlightService/ReserveSeats","code":"OK","latency_ms":13}
```

`LOG_LEVEL=warn` 起两个服务，跑搜索 / 下单 / 404 三类请求：`"msg":"request"` 与 `"msg":"rpc"` 各 **0 行**，而熔断打开、5xx 仍然可见：

```json
{"level":"WARN","msg":"circuit breaker state changed","from":"CLOSED","to":"OPEN","error_count":5,"error_threshold":5}
{"level":"ERROR","msg":"request","status":500,"retries":3,"downstream_code":"Unavailable","error":"search flights: ... all 3 retries exhausted: ..."}
{"level":"ERROR","msg":"request","status":503,"cb":"open","reason":"circuit_open"}
```

恢复路径（`LOG_LEVEL=info`）：`OPEN → HALF_OPEN → CLOSED` 两条 Info 独立行，探测请求的汇总行带 `"cb":"half_open"`。

## 学到什么

**"不在正常请求路径上打日志"这条老规则是错的**，它把日志与指标当成了替代关系。真正的问题从来不是"打不打"，而是两件事：**关得掉吗**（`LOG_LEVEL`），以及**一次请求打几条**。改造前 11 行里有 8 行在重复描述同一件事——删掉它们不损失任何信息，因为调用路径已经在 `%w` 的 wrap 链里了。

**级别是一张判断表，判断表就是会写错。** 改造前 handler 把"座位不足"打成 `Warn`，和压测把 409 算进错误率是同一个错误的两个面。这类错误只有表驱动测试能钉住——所以本轮给两个服务的入口层都补了级别映射测试。
