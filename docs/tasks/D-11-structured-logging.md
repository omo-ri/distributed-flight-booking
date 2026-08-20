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
3. **噪音**：`flight-service/internal/auth/interceptor.go:38` 每个请求成功都打一行 `[AUTH] OK`；cache 每次 HIT/MISS/SET/DEL 都打日志。这些在压测量级下会淹没真正有用的信息，也是可观测的性能开销。

**结果**：出了故障只能靠 `docker compose logs` 肉眼翻，这不是运维手段。
