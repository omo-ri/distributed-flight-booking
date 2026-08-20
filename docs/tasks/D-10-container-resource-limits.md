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
