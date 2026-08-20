---
id: D-02
title: 错误率 SLI 包含客户端错误
severity: critical
status: todo
phase: 0
blocks: []
refs:
  - alerts.yml
  - booking-service/internal/metrics/metrics.go
  - flight-service/internal/metrics/metrics.go
  - prometheus/alerts.yml
  - scripts/demo_alerts.sh
  - scripts/verify_metrics.py
---

# D-02 错误率 SLI 包含客户端错误

**位置**：
- `booking-service/internal/metrics/metrics.go:56-60` —— 4xx 记为 `client_error` 并计入 `http_request_errors_total`
- `flight-service/internal/metrics/metrics.go:49-51` —— 所有非 OK 的 gRPC code 都计入错误
- `prometheus/alerts.yml` —— `HighErrorRate` 对 `http_request_errors_total` 全量求和，不区分 `error_type`

**后果**：可用性 SLI 和 `HighErrorRate`（critical 级）会被客户端行为触发。服务完全健康时，一个写错 URL 的爬虫就能让 SRE 半夜被叫醒。

**这个仓库里已经有证据**：`scripts/demo_alerts.sh` 的 `high-error-rate` 场景，触发手段是**循环请求一个不存在的订单 ID 刷 404**。脚本作者用它来演示告警能响 —— 但它同时精确地演示了这条告警测的不是服务健康度。

**修法**：SLI 的分子只取 `error_type="server_error"`（HTTP 5xx / gRPC `INTERNAL`、`UNAVAILABLE`、`DEADLINE_EXCEEDED`）。4xx 继续采集（它是有用的信号 —— 客户端集成出问题了），但放在单独的告警里，级别降到 warning。

需要同步改：`alerts.yml`、`scripts/verify_metrics.py`、`README.md` 的 SLI 表、Grafana 看板的错误率面板。

## 验收标准

- 循环刷 100 次 404，`HighErrorRate`（critical）**不触发**
- 注入一次真实 5xx，`HighErrorRate` 正常触发
- `scripts/verify_metrics.py` 的错误率查询只统计 `error_type="server_error"`
- 4xx 有独立的 warning 级告警，且 Grafana 上仍能看到 4xx 曲线
