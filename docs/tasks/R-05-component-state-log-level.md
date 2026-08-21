---
id: R-05
title: 组件状态行一律 Warn，并解掉熔断器对全局 logger 的耦合
severity: major
status: todo
phase: foundation
blocks: []
refs:
  - CLAUDE.md
  - booking-service/internal/circuitbreaker/breaker.go
---

# R-05 组件状态行一律 Warn，并解掉熔断器对全局 logger 的耦合

**位置**：`booking-service/internal/circuitbreaker/breaker.go:117-135`；规则本身在 `CLAUDE.md` § 4 的级别表。

**这条要改 `CLAUDE.md`，按 § 1 走单独 PR。**

## 现象 / 触发场景

`breaker.go:120-127` 照着现行规则实现：熔断**翻开**打 `Warn`、**恢复**打 `Info`。叠加上「容量基线压测用 `LOG_LEVEL=warn`」（[`plans/loadtest-observability.md`](../plans/loadtest-observability.md) 定的口径），结果是：

> 压测日志里，你会看到熔断打开，然后**永远看不到它关闭**——一个没有终点的状态机轨迹。

而压测恰恰是最容易把熔断打开的场景。事后翻日志只能看到「它开了」，「开了多久」「什么时候恢复的」全部消失。指标能补一部分（状态 gauge），但分辨率是抓取间隔，而**「故障持续了多久」是复盘报告的核心数字**。

第二个问题在同一个函数里：`breaker.go:129` 用 `slog.Default()` + `context.Background()`，绕过了注入的 logger。想在测试里断言这行就得替换全局状态，而全局状态在 `-race` 的并行测试里是共享的。

## 根因

**「级别按 outcome 判」这条判据是为请求路径设计的，套到组件状态行上就失灵了。**

请求的 outcome 有「成功 / 拒绝 / 失败」的真实语义差异；而状态迁移的两端是**同一个人在同一个场景下要看的同一件事**——你不会只关心它开、不关心它关。把它们劈成两个级别，等于让一个可能被关掉的开关切掉状态机轨迹的一半。

## 修法

**1. 改 `CLAUDE.md` § 4**：级别表里补一条——组件状态变更一律 `Warn`，不分翻开 / 恢复。

正当性写清楚：**常态是「不迁移」**。一个健康运行的系统，熔断器一天迁移 0 次，任何一次迁移都是非常态信号——包括恢复，因为「它恢复了」回答的是「刚才那次故障持续了多久」。

现有表述「翻开是 `Warn`、恢复是 `Info`」删掉。

**2. 改 `breaker.go:120-135`**：去掉级别分支，统一 `Warn`。

**3. 注入 logger**：`circuitbreaker.New` 收一个 `*slog.Logger`，不再用 `slog.Default()`；`context.Background()` 保留（组件状态行确实不属于任何一条请求，这部分是对的）。装配点在 `booking-service/cmd/main.go:88`。

**4. 顺带覆盖同类场景**：[D-05](./D-05-health-check-endpoints.md) 加探针端点之后，探针**成功**要和 `/metrics` 一样排除出汇总行（否则 K8s 每几秒一次，Loki 里九成的行是探针），但探针**失败**要打行——那是组件状态变更且会导致容器重启，同样 `Warn`。本条只在 `CLAUDE.md` 里把规则写进去，实现归 D-05。

## 验收标准

- **怎么验证它生效了**：单测断言 closed→open 与 open→closed 两次迁移都是 `Warn`，且用的是**注入的** logger（不再需要替换 `slog.Default()`）
- 手工跑一次：`LOG_LEVEL=warn` 起栈，打断 flight-service 让熔断翻开再恢复，两条状态行都在
- `CLAUDE.md` § 4 与 [`conventions/engineering.md`](../conventions/engineering.md) 的相关段落不再自相矛盾
- `go test -race ./...` 过
- **怎么知道它出问题了**：压测日志里出现 open 而没有对应的 closed
- **怎么回滚**：`CLAUDE.md` 与 `breaker.go` 一起改回来——规则和实现必须同进同退

## 注意事项

这条改的是**规范**，所以先改 `CLAUDE.md` 再改代码，不能反过来。规则和实现在同一个 PR 里，评审能一眼看到「规则改了什么、代码跟着改了什么」。

## 学到什么

**一条判据的适用范围，和它本身一样重要。** 「级别按 outcome 判」是好规则，但它隐含了一个前提——被判的东西有 outcome。组件状态迁移没有 outcome，它只有「发生了」。规则写下来的时候没人会去标注适用范围，于是它被无差别地套到了下一个看起来相似的场景上。

**规则失灵通常不表现为「规则错了」，而表现为「照着做，结果不对」。**
