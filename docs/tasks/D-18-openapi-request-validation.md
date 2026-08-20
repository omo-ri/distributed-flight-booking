---
id: D-18
title: OpenAPI 里的请求校验约束没有生效
severity: minor
status: todo
phase: backlog
blocks: []
refs:
  - booking-service/api/openapi.yaml
  - booking-service/cmd/main.go
  - booking-service/internal/handler/booking.go
---

# D-18 OpenAPI 里的请求校验约束没有生效

**位置**：`booking-service/cmd/main.go:90-104`（中间件链）、`booking-service/api/openapi.yaml:299`、`booking-service/internal/handler/booking.go:95`

## 现象 / 触发场景

`openapi.yaml` 里给请求体写了校验约束：

```yaml
seat_count:
  type: integer
  minimum: 1          # openapi.yaml:299
user_id:
  type: string
  format: uuid
```

但 Echo 的中间件链里**没有注册任何 OpenAPI 请求校验中间件**——只有四个：

```go
e.Use(middleware.RequestID())   // main.go:91
e.Use(requestLogger(log))       // :94
e.Use(middleware.Recover())     // :97
e.Use(metrics.Middleware())     // :100
```

生成的 `api.gen.go` 只做**路由和绑定**，不做 schema 校验。所以规范里的约束全部是文档，不是行为。

实际生效的只有 handler 里手写的一行：

```go
if req.SeatCount < 1 {          // handler/booking.go:95
    return c.JSON(http.StatusBadRequest, ...)
}
```

**可触发的具体后果**：`user_id` 或 `flight_id` 传一个非 UUID 字符串，请求会一路走到 `uuid.MustParse`（`handler/booking.go:200-202`）或数据库才失败——前者会 panic（被 `middleware.Recover()` 兜成 500），后者是一条 SQL 报错。两种都不是 400。

## 根因

契约优先的做法只用到了一半：用规范**生成了接口**，但没有用规范**执行校验**。于是同一条规则存在于两个互不相干的地方——`openapi.yaml` 里一份、handler 里一份，改了一处另一处不会跟着变。

这正是 [`docs/conventions/`](../conventions/) 里"能生成的东西就不要手写"想避免的漂移，只是漂移发生在规范与实现之间，而不是文档与代码之间。

## 修法

注册 `oapi-codegen` 的请求校验中间件（`github.com/oapi-codegen/echo-middleware` 的 `OapiRequestValidator`），让规范里的约束真正生效，然后删掉 handler 里的手写校验。

**代价必须一起考虑**：

- 校验失败的错误响应由中间件生成，格式不受控，可能和现有的 `api.Error{Message: ...}` 结构不一致——需要提供自定义 `ErrorHandler` 统一格式
- 校验发生在 handler 之前，日志和指标里看到的是中间件的响应而不是业务响应，排障时要知道这一层的存在

## 验收标准

- `POST /bookings` 传 `seat_count: 0` → **400**，且响应体结构与其他 400 一致
- `POST /bookings` 传 `user_id: "not-a-uuid"` → **400**（当前是 500）
- handler 里的手写校验被删除，删除后上面两条仍然通过
- 集成测试覆盖这两条路径

## 学到什么

契约优先有两个独立的收益：**生成代码**（省事）和**执行校验**（正确）。只做前者的话，规范会退化成一份没人验证的文档——而没人验证的文档一定会漂移。判断一个契约是不是"活的"，看它有没有能力让不合规的输入失败。
