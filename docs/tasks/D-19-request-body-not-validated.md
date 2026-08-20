---
id: D-19
title: 请求体必填字段不校验，缺字段变成零值
severity: minor
status: todo
phase: backlog
blocks: []
refs:
  - booking-service/api/openapi.yaml
  - booking-service/cmd/main.go
  - booking-service/internal/handler/booking.go
---

# D-19 请求体必填字段不校验，缺字段变成零值

**位置**：
- `booking-service/internal/handler/booking.go:89-98` —— `c.Bind` 之后只显式校验了 `seat_count`
- `booking-service/cmd/main.go:104` —— `api.RegisterHandlers` 只注册路由，没有挂契约校验中间件

## 现象

契约里 `CreateBookingRequest` 的五个字段全是 `required`，但缺字段不会被拒：

```bash
# 漏掉 flight_id
curl -s -w '\n[%{http_code}]\n' -X POST http://localhost:8080/bookings \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"550e8400-e29b-41d4-a716-446655440000","passenger_name":"A","passenger_email":"a@b.c","seat_count":1}'
{"message":"flight not found"}
[404]
```

缺失的 `flight_id` 被解析成全零 UUID，然后拿这个 ID 去查航班 —— 于是客户端收到的是"航班不存在"，而真实原因是"你少传了一个字段"。同理漏 `user_id` 会创建一个挂在 `00000000-0000-0000-0000-000000000000` 名下的订单，**下单成功，201**。

第二条路径更误导 —— 忘记 `Content-Type: application/json`：

```bash
curl -s -w '\n[%{http_code}]\n' -X POST http://localhost:8080/bookings \
  -d '{"user_id":"…","flight_id":"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11","passenger_name":"A","passenger_email":"a@b.c","seat_count":2}'
{"message":"seat_count must be at least 1"}
[400]
```

Echo 不解析 body，全字段归零，唯一有显式校验的 `seat_count` 第一个撞上 —— 报错和真实原因毫无关系。

## 根因

`c.Bind` 把 JSON 解到结构体上，Go 的零值语义让"没传"和"传了零值"不可区分。生成代码只生成类型和路由，**不生成 body 校验** —— 契约里的 `required` / `minimum` 当前没有任何东西在执行它。校验散落在 handler 里手写，写一条漏四条。

`passenger_email` 同样不做格式校验，`"a"` 能存进库。

## 修法

挂 `oapi-codegen` 配套的请求校验中间件（`OapiRequestValidator`，基于 kin-openapi），用契约本身当校验规则：`required` 缺失、`seat_count` 违反 `minimum: 1`、UUID 格式错，都在进 handler 之前返回 400。handler 里手写的 `seat_count` 检查随之删掉——**规则只写在契约里一处**。

顺带在契约里给 `passenger_email` 加 `format: email`、给两个 `passenger_*` 加 `maxLength: 200`（与 `bookings` 表的 `VARCHAR(200)` 对齐，否则超长会变成 500）。

同一个中间件也是 [D-18](./D-18-invalid-argument-mapped-to-500.md) 的一半修法，两条一起做成本更低。

## 验收标准

- 缺任一必填字段 → 400，错误信息指出**是哪个字段**，不再是 404 / 201
- `seat_count: 0` → 400（由契约的 `minimum` 产生，handler 里不再有这段手写校验）
- 不带 `Content-Type` → 400，且信息指向 content-type 而不是 `seat_count`
- `passenger_email: "a"` → 400；201 字符的 `passenger_name` → 400 而不是 500
- 正常下单路径的请求/响应不变，现有 pytest 全绿
