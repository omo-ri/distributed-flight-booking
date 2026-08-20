---
id: D-12
title: Alertmanager 没有接收端
severity: minor
status: todo
phase: 4
blocks: []
refs:
  - alertmanager/alertmanager.yml
---

# D-12 Alertmanager 没有接收端

`alertmanager/alertmanager.yml` —— receiver 是空的，注释里承认了。告警只在 Alertmanager UI 里显示，不会触达任何人。

作业演示够用，但"告警"的定义就是**主动触达**。没有触达渠道，等于没有告警。
