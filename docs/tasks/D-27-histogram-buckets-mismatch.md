---
id: D-27
title: 直方图桶与实际延迟量级不匹配，p95 数值不可信
severity: minor
status: todo
phase: 1
blocks: []
refs:
  - booking-service/internal/metrics/metrics.go
  - flight-service/internal/metrics/metrics.go
  - scripts/verify_metrics.py
  - prometheus/alerts.yml
---

# D-27 直方图桶与实际延迟量级不匹配，p95 数值不可信

**位置**：`booking-service/internal/metrics/metrics.go`、`flight-service/internal/metrics/metrics.go`（两处 `RequestDuration` 的 `Buckets`）

## 现象 / 触发场景

两个服务的延迟直方图用的是同一组桶：

```go
Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}
```

**最小的桶是 5ms。** 而实测延迟分布（`docs/reports/load/2026-05-29-baseline.md`）：

| 统计量 | 值 |
|---|---|
| 中位数 | **0.92 ms** |
| p95 | **4.38 ms** |
| p99 | 8.60 ms |

**中位数、p95 全部落在第一个桶 `le=0.005` 里面。** 也就是说 95% 以上的样本挤在同一个桶。

`histogram_quantile()` 在桶内做**线性插值**，它假设样本在桶内均匀分布。当 95% 的样本挤在 `[0, 5ms)` 这一个桶时，这个假设完全不成立——**算出来的 p95 基本是插值编出来的数字，不反映真实分布**。

**具体后果**：

1. **CI 门禁比较的是一个不精确的数**。`scripts/verify_metrics.py` 查 Prometheus 拿 p95 来判断 SLO 是否达标（`load-test` job）。真实延迟从 0.9ms 涨到 4.5ms 是**五倍的性能退化**，但两者都落在第一个桶里，算出的 p95 可能几乎不变——**门禁看不见它**。
2. **`HighLatencyP95` 告警的阈值（>1s）不受影响**（那个量级桶是够的），但看板上的延迟分位曲线在正常范围内是失真的，**没法用来做趋势判断**。

## 根因

桶边界照搬了 Prometheus 客户端库的默认值（`prometheus.DefBuckets` 就是这一组）。默认值是为"典型 Web 服务，延迟几十到几百毫秒"设计的，而这个系统是**同机 Docker 网络上的 Go 服务，延迟在亚毫秒量级**——差了两个数量级。

`CLAUDE.md` § 4 明确写了这一条：*"直方图的桶要覆盖实际分布，不照抄默认值。"* 这里正是照抄了默认值。

（注：`docs/architecture/capabilities.md` 的 D-3 把"直方图桶按实际延迟量级选取"记成了已具备能力 ✅。按实测数据这条不成立，修完本条后需要一并订正。）

## 修法

按实测分布重设桶，覆盖亚毫秒到几秒：

```go
Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}
```

**两个必须一起考虑的代价**：

1. **时间序列数增加**。每个桶是一条独立序列，总数 = 桶数 × endpoint 数 × service 数。从 10 个桶加到 13 个是 +30%，Prometheus 内存跟着涨——而这台机器只有 7.7 GiB 且要装 13 个容器（相关：[D-10](./D-10-container-resource-limits.md)、[D-09](./D-09-prometheus-persistence.md)）。桶不能无限加。
2. **改桶会让历史数据不可比**。旧数据的桶边界不同，跨越改动时间点的 `histogram_quantile` 查询会失真。这是**不可逆的**——已经采集的数据无法按新桶重算。所以改动时机应该选在一个明确的边界（比如一次发布），并在 `docs/reports/` 里留一份记录说明哪个时间点之后的数据用的是新桶。

**桶的选取应该有依据而不是再拍一次**：先跑一次去掉 `sleep` 的真实压测拿到分布，再按分布定桶。这依赖 [D-16](./D-16-capacity-load-testing.md)——当前的基线（`k6/script.js:82` 的 `sleep(0.1)`）测到的是 k6 自己的天花板，不是系统的真实延迟分布。

## 验收标准

- 压测运行后，`http_request_duration_seconds_bucket` 的样本**不再有单个桶容纳 90% 以上的观测值**
- 人为在 handler 里注入 3ms 延迟后重跑压测：Prometheus 算出的 p95 有**可见的、量级正确的**变化（当前这个变化会被第一个桶吞掉）
- `scripts/verify_metrics.py` 输出的 p95 与 k6 客户端侧 `http_req_duration p(95)` 在同一量级（当前两者的可比性无法验证）
- `docs/architecture/capabilities.md` 的 D-3 条目按实际情况订正
- `docs/reports/` 下新增一份记录，写明桶变更的时间点与前后不可比

## 学到什么

**一个指标存在，不等于它能回答你的问题。** 这套系统有延迟直方图、有 p95 查询、有基于 p95 的 CI 门禁和告警——整条链路看起来完备，但因为桶选错了，链路最上游的**数据本身**就没有承载所需的分辨率。下游做得再对也补不回来。

判据可以固定下来：**看到一个直方图指标，先看它的桶边界落在实测分布的哪个位置。** 如果绝大多数样本挤在第一个或最后一个桶，这个指标算出的所有分位数都不可信——**它不是"不够精确"，是"没有信息"**。

更一般地：这是"监控系统自己也需要被测试"的一个实例。`docs/design/system-design.md` § 8.4 讲了为什么 CI 门禁要查 Prometheus 而不是直接看 k6 输出——那条路径顺带验证了指标链路是通的。但"通"和"准"是两回事，本条正是链路通了但数据不准。
