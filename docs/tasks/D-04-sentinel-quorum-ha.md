---
id: D-04
title: Redis Sentinel 不构成高可用
severity: major
status: todo
phase: 2
blocks: []
refs:
  - docker-compose.yml
---

# D-04 Redis Sentinel 不构成高可用

**位置**：`docker-compose.yml:58-79`

```
sentinel monitor mymaster redis-master 6379 1
                                            ↑ quorum = 1
```

只部署了 **1 个 sentinel 实例**，quorum 设为 1。

**两个问题**：
1. **Sentinel 自身是单点** —— 它挂了就没有 failover 能力，整个"高可用"归零
2. **quorum=1 无法防脑裂** —— 单个 sentinel 因为网络抖动误判 master 下线，就能单方面发起 failover，可能产生两个 master

**正确形态**：3 个 sentinel，quorum=2。奇数个节点 + 过半数投票，这是所有基于 quorum 的分布式系统（etcd、ZooKeeper、Consul）的通用规律。

**学习价值高**：修完之后可以做一次真实演练 —— 杀掉 master，观察 sentinel 选举日志、failover 耗时、客户端重连时间窗内丢了多少请求。这是很好的复盘素材。
