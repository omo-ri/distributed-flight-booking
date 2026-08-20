---
id: D-09
title: 监控数据不持久
severity: minor
status: todo
phase: 1
blocks: []
refs:
  - docker-compose.yml
---

# D-09 监控数据不持久

`docker-compose.yml:163` —— `--storage.tsdb.retention.time=1h`，且 Prometheus 和 Grafana **都没有 volume**。

容器一重启，所有历史指标丢失。1 小时保留期意味着无法做任何跨天的趋势分析、容量规划、SLO 周期统计（error budget 通常按 28 天算）。

**修法**：加 volume；保留期至少 15 天；后续考虑 remote write 到长期存储。
