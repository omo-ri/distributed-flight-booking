---
id: D-05
title: 没有健康检查端点
severity: major
status: todo
phase: 0
blocks: [phase-2]
refs:
  []
---

# D-05 没有健康检查端点

全仓库没有 `/health`、`/ready`、`/livez`。Compose 的 healthcheck 只覆盖 PostgreSQL 和 Redis，两个 Go 服务没有 healthcheck。

K8s 的 liveness / readiness probe 无处可接。当前只能拿 `/metrics` 凑合，但 `/metrics` 能返回 200 不代表服务能干活 —— 数据库连接断了、下游熔断打开了，`/metrics` 照样 200。

**修法**：区分两类端点，这个区分是面试高频题：

| 端点 | 语义 | 检查内容 | 失败后果 |
|---|---|---|---|
| `/livez` | 进程还活着吗 | 极轻量，只证明事件循环没死锁 | K8s **重启** Pod |
| `/readyz` | 现在能接流量吗 | DB ping、Redis ping、必要下游可达 | K8s **摘除**流量，不重启 |

**最容易犯的错**：把依赖检查放进 liveness。数据库短暂抖动 → liveness 失败 → K8s 重启所有 Pod → 重启后依然连不上 → 无限重启（CrashLoopBackOff），一次小抖动被放大成全站故障。

## 验收标准

- 两个服务都有 `/livez` 和 `/readyz`，且**语义不同**：停掉数据库后 `/readyz` 返回 503 而 `/livez` 仍返回 200
- compose 里两个 Go 服务都配上 healthcheck，指向 `/livez`
- 依赖检查**不出现在** liveness 里（这条要能在 code review 时说清楚理由）
- `/readyz` 的检查项有超时，不会因为下游慢而把自己拖挂
