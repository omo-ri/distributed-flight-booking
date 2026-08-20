---
id: D-20
title: 5xx 响应把内部错误原文返回给客户端
severity: minor
status: todo
phase: backlog
blocks: []
refs:
  - booking-service/internal/handler/booking.go
---

# D-20 5xx 响应把内部错误原文返回给客户端

**位置**：`booking-service/internal/handler/booking.go` 的六个兜底分支 —— `:58`、`:83`、`:127`、`:148`、`:160`、`:189`，全都是 `api.Error{Message: err.Error()}`

## 现象

数据库错误连同 SQLSTATE 一起进了公开响应体：

```bash
curl -s -X POST http://localhost:8080/bookings -H 'Content-Type: application/json' \
  -d '{"user_id":"550e8400-e29b-41d4-a716-446655440000","flight_id":"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
       "passenger_name":"<201 个字符>","passenger_email":"a@b.c","seat_count":1}'
{"message":"create booking: ERROR: value too long for type character varying(200) (SQLSTATE 22001)"}
```

gRPC 层的错误同样原样透出（见 [D-18](./D-18-invalid-argument-mapped-to-500.md)）：

```json
{"message":"search flights: rpc error: code = InvalidArgument desc = origin and destination are required"}
```

外部调用方由此能看到：底层是 PostgreSQL、列类型和长度上限、内部服务用 gRPC、下游方法名、以及内部错误包装的调用链。数据库连接失败时，`err.Error()` 里还会带上主机名和用户名（DSN 的一部分）。

## 根因

`err.Error()` 是给**排障的人**看的，`message` 字段是给**调用方**看的 —— 两个受众被当成了一个。区分它们的位置就在 handler：往下走进日志（已经有了 `h.log.Error(...)`，信息一条不少），往外走给客户端一句稳定的话。

## 修法

5xx 分支统一返回固定文案（`"internal error"`），把 `err` 只留在 `h.log.Error` 里。客户端要定位问题，靠已有的 `X-Request-ID`（`booking-service/cmd/main.go:91` 已挂 `middleware.RequestID`，`:134` 已把它写进日志）—— 把这个 ID 放进响应头/响应体，比透出 SQLSTATE 有用得多，也不泄漏任何东西。

4xx 分支不受影响：`"flight not found"`、`"insufficient seats"` 这类是**业务语义**，本来就该告诉调用方。

## 验收标准

- 触发一次 5xx（超长 `passenger_name` 即可），响应体不含 `SQLSTATE`、`rpc error`、表名列名、主机名
- 同一次请求在日志里能查到完整错误原文，且带 `request_id`
- 响应能让调用方拿到 `request_id`，凭它在日志里定位到这次失败
- 4xx 的业务错误文案保持不变，现有 pytest 全绿
