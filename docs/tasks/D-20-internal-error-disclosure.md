---
id: D-20
title: 内部错误详情被原样返回给外部客户端
severity: major
status: todo
phase: 0
blocks: []
refs:
  - booking-service/internal/handler/booking.go
---

# D-20 内部错误详情被原样返回给外部客户端

**位置**：`booking-service/internal/handler/booking.go:58`、`:83`、`:127`、`:148`、`:160`、`:189`

## 现象 / 触发场景

六处 500 响应都是同一行：

```go
return c.JSON(http.StatusInternalServerError, api.Error{Message: err.Error()})
```

`err` 是一路 `%w` 包装上来的原始错误，`err.Error()` 展开后会包含：

- **PostgreSQL 的原始报错**：约束名（`chk_available_le_total`）、列名、表名，有时还有出错的值
- **gRPC 的传输层错误**：`rpc error: code = Unavailable desc = connection error: dial tcp 172.18.0.5:50051: connect: connection refused` —— **内部服务的容器 IP、端口和拓扑直接暴露**
- **服务间的内部错误文本**，包括 flight-service 用 `status.Errorf(codes.Internal, "reserve seats: %v", err)` 拼进去的下游细节（`flight-service/internal/handler/flight.go:74` 等）

booking-service 是**唯一的公开入口**（见 `docs/design/system-design.md` § 2.4），所以这些字符串会直接出现在公网响应体里。

## 根因

把"给开发者看的错误"和"给调用方看的错误"当成了同一个东西。前者需要尽可能详细，后者需要尽可能少——**两者的受众和信任级别不同，不该共用一个字符串**。

当前 `err.Error()` 同时承担了日志内容和响应内容两个职责，而它只在日志那一侧是合适的。

## 修法

500 响应体的 `Message` 换成固定文案（`"internal error"`）加一个 `request_id`，真实错误只写进日志。

**前置依赖**：`request_id` 要真的能查到东西。当前 `middleware.RequestID()` 只在 booking-service 的边界生成（`booking-service/cmd/main.go:91`），**没有透传到 gRPC metadata**，所以 flight-service 的日志里没有它——跨服务查不通。这一半属于 [D-11](./D-11-structured-logging.md)。

所以拆成两步：

1. **本条**：500 不再回显 `err.Error()`，改为固定文案 + `request_id`，错误全文进 `slog.Error`
2. **D-11 之后**：`request_id` 透传到 gRPC metadata，两个服务的日志能用同一个 ID 串起来

4xx 的文案不受影响——它们本来就是手写的固定文案（`"insufficient seats"`、`"flight not found"`），这是对的。

## 验收标准

- `docker compose stop flight-service` 后调 `POST /bookings`，响应体**不含** IP、端口、`rpc error`、表名、约束名
- 同一次请求的响应头 `X-Request-Id` 能在 booking-service 的日志里查到，且日志里有完整的原始错误
- 6 处调用点全部改完（`grep -n 'err.Error()' booking-service/internal/handler/` 应无匹配）
- 4xx 响应的文案不变（回归）

## 学到什么

**信息泄漏很少来自一个显眼的漏洞，通常来自一个方便的默认写法。** `err.Error()` 是 Go 里最自然的一行代码，而它恰好把内部拓扑写进了公网响应。

判据可以固定下来：任何要发到进程之外的字符串，都要问一次"接收方有资格看到这个吗"。同一个错误对内应该更详细、对外应该更简略，两者用 `request_id` 关联——这也是为什么 [D-11](./D-11-structured-logging.md) 不只是"日志好看一点"的问题：**没有可关联的日志，对外简化就等于把排障能力一起丢掉。**
