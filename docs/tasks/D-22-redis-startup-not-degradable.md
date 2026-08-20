---
id: D-22
title: Redis 连接失败导致 flight-service 无法启动
severity: major
status: todo
phase: 0
blocks: []
refs:
  - flight-service/cmd/main.go
  - flight-service/internal/service/flight.go
---

# D-22 Redis 连接失败导致 flight-service 无法启动

**位置**：`flight-service/cmd/main.go:51-67`

## 现象 / 触发场景

缓存在**运行期**是可选的——`flightService` 的每一处缓存调用都有 `if s.cache != nil` 保护（`flight-service/internal/service/flight.go:29`、`:45`、`:56`、`:72`、`:107`），Redis 中途挂掉时 `Get` 报错会被当成 MISS（`flight-service/internal/cache/redis.go:62-65`），请求照常走数据库返回。**降级路径完整且正确。**

但在**启动期**是强制的：

```go
if sentinelAddr := os.Getenv("REDIS_SENTINEL_ADDR"); sentinelAddr != "" {
    redisCache, err = cache.NewRedisSentinelCache(sentinelAddr, masterName)
    if err != nil {
        log.Fatalf("connect to redis sentinel: %v", err)   // ← main.go:55
    }
} else if addr := os.Getenv("REDIS_ADDR"); addr != "" {
    redisCache, err = cache.NewRedisCache(addr)
    if err != nil {
        log.Fatalf("connect to redis: %v", err)            // ← main.go:61
    }
}
```

**这个不对称是危险的**：Redis 挂掉时服务活着（降级运行），但**这时候只要重启一次，服务就再也起不来了**。

而"服务出问题 → 重启试试"是运维最常见的第一反应。所以真实的事故序列是：

```
Redis 故障 → flight-service 降级运行（变慢，因为读全落到 PG）
          → 有人觉得"变慢了，重启一下"
          → flight-service 起不来
          → 从「性能下降」升级成「完全不可用」
```

K8s 环境下更糟：容器 crash 后会被自动重启，进入 `CrashLoopBackOff`——**没有人为干预也会走到这一步**。这条会在阶段 2 迁移后立刻变成生产事故形态。

同样的问题在 `sentinel` 侧被放大：客户端只配了**一个** sentinel 地址（`cache/redis.go:26`：`SentinelAddrs: []string{sentinelAddr}`），sentinel 自己挂了也会让 `NewRedisSentinelCache` 失败——即使 Redis master 完好无损。相关：[D-04](./D-04-sentinel-quorum-ha.md)。

## 根因

把"启动时依赖不可用"和"依赖是必需的"划了等号。判据应该是**这个依赖不在时，服务还能不能提供有意义的功能**——缓存的答案是"能，只是慢"，所以它不该是启动的硬依赖。

数据库是真正的硬依赖（没有它 flight-service 什么都做不了），`log.Fatalf` 在那里是对的（`main.go:45`）。**两者被同样处理了，但它们不是一类东西。**

## 修法

Redis 连接失败时记 `Warn` 并以无缓存模式启动（`redisCache` 保持 `nil`，下游的 `if s.cache != nil` 分支本来就完整支持这个），不再 `Fatalf`。

**必须一起做的两件事**，否则修完会引入更隐蔽的问题：

1. **降级状态要可见**。暴露一个 `cache_enabled` gauge（0/1）并配告警，否则会出现"服务悄悄以无缓存模式运行了三天没人知道"——那比起不来更难发现。这也是 `CLAUDE.md` § 4"每个韧性组件都要有指标"的直接要求。
2. **要能自愈**。以无缓存模式启动后，Redis 恢复了服务也不会自动接上。要么加后台重连，要么明确接受"需要一次重启才能恢复缓存"并写进 runbook（[D-14](./D-14-alert-runbooks.md)）。

顺带一提：把启动期的失败改成降级之后，**按 `docs/design/system-design.md` § 6.4 的分析，无缓存模式下读负载会全部压到 PostgreSQL 并与写路径争抢 `pgxpool` 的连接**——所以这条不能单独理解成"提高可用性"，它只是把故障形态从"起不来"换成"很慢"。真正的容量问题在 [D-10](./D-10-container-resource-limits.md) 和 [D-16](./D-16-capacity-load-testing.md)。

## 验收标准

- `docker compose stop redis-master redis-sentinel` 后 `docker compose restart flight-service` → 服务**正常启动**，日志里有一条 Warn 说明进入无缓存模式
- 此状态下 `GetFlight` / `SearchFlights` 仍返回正确结果（走数据库）
- `cache_enabled` 指标为 0，且 Grafana 上能看到
- Redis 恢复后：要么服务自动接回缓存（`cache_enabled` 回到 1），要么 runbook 里明确写了需要重启
- 数据库不可用时服务**仍然**启动失败（回归——硬依赖的行为不能一起改掉）

## 学到什么

**"依赖不可用时怎么办"这个问题，启动期和运行期必须分别回答，而且很容易只答了一半。** 这个仓库里运行期答得很好（完整的 `nil` 检查和 MISS 降级），启动期完全没答——因为写启动代码时想的是"连不上就没法工作"，写业务代码时想的是"缓存拿不到就查库"，两处的心智模型不一样，中间没人对齐。

判据可以固定成一句话：**一个可选依赖，在任何时刻不可用都不应该导致进程无法存在。** 如果它能被降级，那降级路径必须覆盖进程的整个生命周期，而不只是覆盖请求处理。
