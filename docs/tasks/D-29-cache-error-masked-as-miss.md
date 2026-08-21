---
id: D-29
title: 缓存故障伪装成未命中
severity: minor
status: todo
phase: 1
blocks: []
refs:
  - flight-service/internal/cache/redis.go
---

# D-29 缓存故障伪装成未命中

**位置**：`flight-service/internal/cache/redis.go` 的 `GetFlight` / `GetSearch`

```go
val, err := c.client.Get(ctx, key).Bytes()
if err != nil {
    record(ctx, metrics.CacheFlight, metrics.OpGet, metrics.ResultMiss)
    return nil, false
}
```

## 现象 / 触发场景

`redis.Nil`（键不存在，真未命中）与连接失败、超时、Sentinel 选主中被一律当成 MISS。降级行为本身是对的——请求照常走数据库返回，这是 [D-22](./D-22-redis-startup-not-degradable.md) 说的那条完整降级路径。**问题在可观测性**：

- 指标里 `result` 永远是 `miss`，`error` 这个取值在读路径上一次都不会出现
- 汇总行上的 `cache` 字段同样只会是 `hit` / `miss`
- 于是"Redis 挂了"在看板上表现为**命中率跌到 0**，与"缓存刚过期、正在回填"长得一模一样

真出故障时，最需要区分的正是这两者：一个要人介入，一个不用。

## 修法

`errors.Is(err, redis.Nil)` 判真未命中，其余错误计 `result="error"` 并抬到 `Warn`（异常但已自动处理，与写路径的 `cache_set_failed` 同构）。指标标签 `result` 已经给 `error` 留好了位置（`flight-service/internal/metrics/metrics.go`），Grafana 的 `Flight cache — Operations/sec by op and result` 面板会自动多出一条线。

`logctx` 侧需要给 `cache` 字段加第四个取值（`error`），同时更新 [`conventions/engineering.md`](../conventions/engineering.md) 的字段白名单表。

## 验收标准

- `docker compose stop redis-master redis-slave redis-sentinel` 后打一次读请求：指标出现 `flight_cache_operations_total{op="get",result="error"}`，汇总行是 `WARN` 且带 `cache=error`
- 查一个不存在的航班：仍然是 `result="miss"`、`Info`
- 单元测试覆盖两条路径（用假的 redis client，属 L1）

## 学到什么

这条缺陷是补缓存指标时发现的：**加指标会暴露原本被掩盖的语义混淆**。日志时代 `[CACHE] MISS` 也是同样的谎，但没人会去数它；变成指标画到看板上，"命中率跌到 0" 就成了一个要有人解释的现象。
