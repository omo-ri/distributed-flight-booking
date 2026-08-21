---
id: D-16
title: 压测无法回答容量问题
severity: minor
status: todo
phase: 4
blocks: []
refs:
  []
---

# D-16 压测无法回答容量问题

**登记时**的 `k6/script.js` 固定 10 VU / 30s。[T-06](./T-06-k6-three-scenarios.md) 已把脚本改成三场景开环压测、[T-07](./T-07-local-capacity-baseline.md) 负责产出基线报告，本条剩下的是**容量规划**本身。当时它能回答"有没有退化"，回答不了：

- 系统的吞吐拐点在哪？
- 什么资源先成为瓶颈（CPU / PG 连接池 / Redis）？
- 需要几个副本才能撑住 N QPS？

**缺阶梯压测**（ramping-arrival-rate，逐级加压直到 SLO 破线）。没有这个数据，D-10 的资源限制和后续的 HPA 阈值都只能拍脑袋。
