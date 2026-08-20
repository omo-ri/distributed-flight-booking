---
id: D-14
title: 告警没有 runbook
severity: major
status: todo
phase: 4
blocks: []
refs:
  []
---

# D-14 告警没有 runbook

三条告警规则的 annotations 只有 summary 和 description，**没有 `runbook_url`**。

告警响了，值班的人打开一看"HighErrorRate on booking-service"，然后呢？查什么？看哪个看板？常见原因是什么？怎么止血？

**运维岗对这个的重视程度远超开发岗**。每条告警必须对应一份 runbook：影响面 → 快速诊断步骤 → 常见原因 → 止血手段 → 升级路径。
