---
id: D-10
title: 容器没有资源限制
severity: minor
status: todo
phase: 2
blocks: []
refs:
  - docker-compose.yml
---

# D-10 容器没有资源限制

`docker-compose.yml` 里 **0 处** `mem_limit` / `cpus` / `deploy.resources`，也 **0 处** `restart:` 策略。

任何一个容器内存泄漏都能拖垮整台宿主机。K8s 迁移时这会变成必须回答的问题：requests 和 limits 分别设多少？

**这个知识点面试必考**：
- `requests` 决定调度（能不能放进这个节点）和 QoS 分级
- `limits` 决定运行时上限（CPU 被 throttle，内存超了直接 OOMKill）
- 三种 QoS：Guaranteed（requests == limits）/ Burstable / BestEffort，节点内存压力时按倒序驱逐

**怎么定值**：不能拍脑袋，要从压测数据推。这正好接上已有的 k6 基线。

## 磁盘也是资源 ✅

内存和 CPU 之外还有一个没设上限的资源：**日志盘**。docker 默认的 `json-file` driver 不轮转，一次读路径压测（百万级请求）就能把宿主机写满——这不是假设，工作区里躺过 479 MB 的压测残留。

已加 `x-logging` anchor，13 个服务全部 `max-size: 50m` / `max-file: 3`（单容器上限 150 MB）。验证：

```bash
docker compose ps -q | xargs -I{} docker inspect {} \
  --format '{{.Name}} {{index .HostConfig.LogConfig.Config "max-size"}}'
```

2026-08-21 实测 13/13 生效。这只是兜底，不是治法——治法是 `LOG_LEVEL`（[D-11](./D-11-structured-logging.md)）。

**剩下的**：`mem_limit` / `cpus` / `restart:` 仍是 0 处，要等 T-07 的容量基线才有定值依据。
