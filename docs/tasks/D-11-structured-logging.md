---
id: D-11
title: 日志不可用于排障
severity: major
status: todo
phase: 1
blocks: [phase-5]
refs:
  - flight-service/internal/auth/interceptor.go
---

# D-11 日志不可用于排障

三个独立问题：

1. **格式不一致**：booking-service 是 slog JSON，flight-service 是标准库文本（`[CACHE] HIT flight:xxx`）。一套解析规则覆盖不了两个服务。
2. **无关联**：booking-service 有 request_id，但没有通过 gRPC metadata 传给 flight-service。一次跨服务调用在两边的日志里无法串联。
3. **关不掉**：`flight-service/internal/auth/interceptor.go:38` 每个请求成功都打一行 `[AUTH] OK`；cache 每次 HIT/MISS/SET/DEL 都打日志。**这些日志本身是要的**（CLAUDE.md § 4：请求路径日志打在 `Info`），问题是 flight-service 用标准库 `log.Printf`，没有级别概念，压测时想关也关不掉。实测代价：一次读路径压测 385 万请求 × 每请求至少 2 行 = 770 万行、几个 G。

**结果**：出了故障只能靠 `docker compose logs` 肉眼翻，这不是运维手段。

## 做什么

| # | 动作 | 状态 |
|---|---|---|
| 1 | booking-service `LOG_LEVEL` 环境变量（`cmd/main.go:29` + `parseLogLevel`），compose 传 `${LOG_LEVEL:-info}` | ✅ 已做 |
| 2 | flight-service `log.Printf` → `slog` + JSON + `LOG_LEVEL`，与 booking 侧同构 | todo |
| 3 | trace_id 跨 gRPC metadata 贯通 | todo |

第 2 点的逐处改法见 [`plans/loadtest-observability.md`](../plans/loadtest-observability.md) 阶段二。
