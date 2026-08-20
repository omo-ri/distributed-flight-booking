---
id: D-18
title: 下游 INVALID_ARGUMENT 映射成 500
severity: major
status: todo
phase: 0
blocks: []
refs:
  - booking-service/api/openapi.yaml
  - booking-service/internal/handler/booking.go
  - flight-service/internal/handler/flight.go
  - docs/architecture/contracts.md
---

# D-18 下游 INVALID_ARGUMENT 映射成 500

**位置**：
- `booking-service/internal/handler/booking.go:52-59` —— `SearchFlights` 的错误分支只认熔断器错误，其余一律 500
- `flight-service/internal/handler/flight.go:26-28` —— 空 `origin` / `destination` 返回 `INVALID_ARGUMENT`
- `booking-service/api/openapi.yaml:12-23` —— `origin` / `destination` 只声明了 `required`，没有 `minLength`

## 现象

客户端传了空串，服务端返回 5xx：

```bash
curl -s -w '\n[%{http_code}]\n' 'http://localhost:8080/flights?origin=&destination=LED'
{"message":"search flights: rpc error: code = InvalidArgument desc = origin and destination are required"}
[500]
```

`?origin=LED`（完全不带 `destination`）走的是生成代码的必填检查，正确返回 400。只有"带了参数但值为空"这条路径漏到了 500。

## 根因

两层校验之间有缝：

1. 生成的 wrapper 只检查参数**存在**，不检查值非空 —— 契约里没有 `minLength`，它没有依据去拒绝空串
2. 空串穿到 flight-service 才被拒，回来的 `INVALID_ARGUMENT` 在 booking-service 侧没有对应的映射分支，落进兜底的 500

[`../architecture/contracts.md`](../architecture/contracts.md) 的错误码映射表写的是 `INVALID_ARGUMENT` → 400 —— 那是**设计意图，不是当前实现**。文档已就地标注。

## 后果

不只是状态码难看：500 会被计入 `error_type="server_error"`，直接进可用性 SLI 的分子，也就是**客户端的一个空参数能烧 error budget、能把 `HighErrorRate`（critical）打响**。方向和 [D-02](./D-02-error-rate-sli-server-errors-only.md) 相反，但坏的是同一件事——SLI 测的不是服务健康度。

## 修法

两处都要动，缺一不可：

1. **契约收紧**：`openapi.yaml` 里给 `origin` / `destination` 加 `minLength: 1`，重新生成。契约是源，校验规则应该写在契约里而不是 handler 里。
2. **补映射**：booking-service 侧把 gRPC `INVALID_ARGUMENT` 映射成 400，与 contracts.md 的表对齐。这是纵深防御 —— 第 1 条只挡得住这一个字段，映射缺失是所有下游校验错误的共性问题。

顺带把 `GetFlight` / `CreateBooking` / `CancelBooking` 的兜底分支一起过一遍，它们有同样的缝。

## 验收标准

- `?origin=&destination=LED` 返回 400，且**不产生** `error_type="server_error"` 计数
- 空 `destination`、`origin` 全空白字符（`%20`）同样返回 400
- 正常搜索仍返回 200，行为不变
- contracts.md 错误码表与实现一致（`INVALID_ARGUMENT` → 400 不再是"设计意图"）
- 集成测试覆盖空串参数这条路径，防止回归
