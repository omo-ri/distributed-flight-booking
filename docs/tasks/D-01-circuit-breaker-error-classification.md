---
id: D-01
title: 熔断器把业务错误计入失败统计
severity: critical
status: todo
phase: 0
blocks: []
refs:
  - booking-service/internal/grpcclient/flight.go
---

# D-01 熔断器把业务错误计入失败统计

**位置**：`booking-service/internal/grpcclient/flight.go:66-71`

```go
result, err := retry(ctx, method, fn)
if err != nil {
    cb.RecordFailure()      // ← 任何 err 都算失败
} else {
    cb.RecordSuccess()
}
```

`retry()` 对不可重试的错误（`NOT_FOUND`、`RESOURCE_EXHAUSTED`、`INVALID_ARGUMENT`）会立即返回错误。这些错误随后被 `RecordFailure()` 计入熔断统计。

**触发场景**：60 秒内 5 次查询不存在的航班（正常的用户行为，返回 404），熔断器就会打开。此后 30 秒内**所有请求**——包括完全正常的下单——都被拒绝，返回 503。

一个用户的错误输入，能让整个服务对所有人不可用。

**根因**：混淆了两类错误。
- **依赖健康度信号**：`UNAVAILABLE`、`DEADLINE_EXCEEDED`、`INTERNAL` —— 说明下游有问题
- **业务结果**：`NOT_FOUND`、`RESOURCE_EXHAUSTED` —— 下游工作完全正常，只是答案是"没有"

熔断器要保护的是下游过载，只应该对第一类计数。

**修法**：引入 `isCircuitFailure(err) bool`，只对下游健康度相关的 code 计失败。同时给熔断器加 Prometheus 指标（当前状态 gauge + 状态迁移 counter），否则线上熔断打开了没人知道。

## 验收标准

- 连续请求 100 次不存在的航班（`NOT_FOUND`），熔断器**保持 CLOSED**
- `docker compose stop flight-service` 后，熔断器在阈值内打开，Grafana 上能看到状态 gauge 从 0（CLOSED）跳到 1（OPEN）
- 状态迁移 counter 有数据，能回答"过去 24 小时熔断打开过几次"
- 单元测试覆盖：业务错误不计失败、健康度错误计失败两条路径
