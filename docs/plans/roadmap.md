# 路线图

目标：把这个项目从"一个开发得不错的作业"变成"一个被完整运维过的系统"，同时把目标岗位 JD 的每一条都变成可展示的实践。

**总周期估算：8–12 周**（按每周投入 10–15 小时）。这不是一个必须严格遵守的计划，阶段之间的依赖关系比时间表重要。

## 阶段依赖

```
阶段 0  修复阻塞缺陷 ─────────┐
        (D-03 D-05 D-01 D-02) │
                              ▼
阶段 1  可观测性补全 ────────┐
        (日志 + 链路)         │
                             ▼
阶段 2  Kubernetes ──────────┼──► 阶段 3  持续交付
                             │              │
                             ▼              ▼
                    阶段 4  SRE 方法论 ◄─────┘
                             │
                             ▼
                    阶段 5  混沌工程 + 复盘
                             │
                             ▼
                    阶段 6  自动化 / vibe coding
```

阶段 0 和 1 是**所有后续工作的地基**，不能跳。

---

## 阶段 0 · 修复阻塞缺陷

**时长**：3–5 天 · **前置**：无

先把已发现的、会阻塞后续工作的缺陷修掉。这些改动小、收益直接，而且每一个都是一个完整的知识点。

| 任务 | 缺陷 | 学到什么 |
|---|---|---|
| 两个服务加优雅停机 | [D-03](../tasks/D-03-graceful-shutdown.md) | SIGTERM 处理、在途请求排空、`GracefulStop` vs `Stop`、K8s 终止生命周期与 `preStop` |
| 加 `/livez` 和 `/readyz` | [D-05](../tasks/D-05-health-check-endpoints.md) | liveness 与 readiness 的语义区别；为什么依赖检查放进 liveness 会造成 CrashLoopBackOff |
| 熔断器区分故障类型 | [D-01](../tasks/D-01-circuit-breaker-error-classification.md) | 依赖健康度信号 vs 业务结果；熔断器该保护什么 |
| 熔断器加 Prometheus 指标 | [D-01](../tasks/D-01-circuit-breaker-error-classification.md) | 状态 gauge + 迁移 counter；"没有指标的机制等于不存在" |
| 错误率 SLI 只算 server error | [D-02](../tasks/D-02-error-rate-sli-server-errors-only.md) | SLI 定义的正确性；为什么 4xx 不该进可用性 SLI |
| 容器非 root + `.dockerignore` | [D-15](../tasks/D-15-container-nonroot.md) | 容器安全基线、构建上下文 |

**验收**：
- `docker compose stop booking-service` 期间持续压测，**零请求失败**
- 熔断器 OPEN 状态在 Grafana 上可见
- 连续请求 100 次不存在的航班，熔断器**保持 CLOSED**
- 同样这 100 次 404 **不触发** `HighErrorRate`

**为什么先做这个**：修 bug 比加功能学得深。每个 bug 背后都有一个"为什么当初这么写是错的"的完整推理，这正是面试想听的。

---

## 阶段 1 · 可观测性补全

**时长**：1.5–2 周 · **前置**：阶段 0

补上日志和链路追踪，让"三大支柱"（指标 / 日志 / 链路）在同一个界面里能互相跳转。

### 任务

1. **统一结构化日志** —— flight-service 从标准库 `log` 换成 `slog` JSON；清理高频路径上的噪音日志（[D-11](../tasks/D-11-structured-logging.md)）
2. **trace_id 贯通** —— booking 侧生成，通过 gRPC metadata 传给 flight，两侧日志都带上
3. **OpenTelemetry** —— 两个服务接入 SDK，HTTP 和 gRPC 都自动埋点，导出到 Tempo（或 Jaeger）
4. **Loki + Alloy** —— 日志采集入栈，Grafana 加 Loki 数据源
5. **三支柱联查** —— Grafana 配置 derived fields，从日志的 trace_id 一键跳到链路视图；从看板的异常时间点跳到对应日志
6. **指标补全** —— 缓存命中率、连接池使用率、重试次数
7. **Prometheus 持久化** —— 加 volume，保留期提到 15 天（[D-09](../tasks/D-09-prometheus-persistence.md)）

### 验收

一个可复现的排障演示：**从 Grafana 上一个延迟尖峰出发，三次点击定位到具体那次慢请求的完整跨服务链路和它的日志**。录成 GIF 放进文档。

### 知识点

- OTel 的 Context 传播机制：W3C Trace Context 标准、`traceparent` header、gRPC metadata 载体
- Span 与 Trace 的关系、采样策略（头部采样 vs 尾部采样，各自的取舍）
- Loki 和 ELK 的架构差异：Loki 只索引标签不索引全文，成本低但查询模式受限
- LogQL 基本查询
- 日志标签的基数问题（和 Prometheus 是同一类陷阱）

### 对应 JD

第 5 条（ELK/Loki）、第 6 条（故障定位能力）

---

## 阶段 2 · Kubernetes 迁移

**时长**：2.5–3 周 · **前置**：阶段 0（优雅停机 + 健康检查是硬前置）

**这是最大的缺口，也是投入产出最高的阶段。**

### 任务

**分步走，不要一次性上 Helm。** 先手写 YAML 理解每个字段，再抽象。

1. **本地集群** —— kind 或 k3d，多节点配置（单节点体验不到调度和亲和性）
2. **手写 manifests**
   - 无状态服务：Deployment + Service
   - 数据库：StatefulSet + PVC + Headless Service
   - Redis：3 个 sentinel 的 StatefulSet（顺便修 [D-04](../tasks/D-04-sentinel-quorum-ha.md)）
   - 入口：Ingress（nginx-ingress 或 Traefik）
3. **探针配置** —— liveness / readiness / startup 三种探针的参数调优
4. **资源管理** —— 从 k6 压测数据推 requests/limits（[D-10](../tasks/D-10-container-resource-limits.md)），理解三种 QoS
5. **配置与密钥** —— ConfigMap + Secret，密钥从 compose 里挪出来（[D-07](../tasks/D-07-plaintext-secrets.md)）
6. **可用性保障** —— HPA、PDB、`topologySpreadConstraints`
7. **参数化** —— Kustomize base + overlays（dev/staging），或 Helm chart
8. **安全上下文** —— `runAsNonRoot`、`readOnlyRootFilesystem`、`drop: [ALL]`
9. **监控栈上 K8s** —— kube-prometheus-stack，ServiceMonitor 自动发现

### 验收

- `kubectl apply -k deploy/overlays/dev` 一条命令起完整环境
- 滚动更新期间持续压测，**零错误**（这是阶段 0 优雅停机工作的真正验证）
- `kubectl delete pod` 杀掉任意 Pod，服务自动恢复且无用户可见影响
- HPA 在压测下真实触发扩容
- 数据库 Pod 重建后数据完好

### 知识点

- **Pod 生命周期**：Init → Running → Terminating，各阶段发生什么
- **探针三兄弟**：liveness（重启）/ readiness（摘流量）/ startup（保护慢启动服务不被 liveness 误杀）
- **QoS 与驱逐**：Guaranteed / Burstable / BestEffort，节点内存压力时的驱逐顺序
- **Service 与 kube-proxy**：ClusterIP 怎么实现的，iptables 模式 vs IPVS
- **DNS**：CoreDNS 解析路径、`ndots:5` 造成的额外查询、Service FQDN 格式 —— **这条直接对应 JD 要求 4 的 DNS**
- **StatefulSet 为什么存在**：稳定网络标识、有序启停、PVC 与 Pod 的绑定关系
- **调度**：nodeSelector / affinity / taint & toleration / topologySpreadConstraints
- **网络模型**：每 Pod 一 IP、CNI 职责、NetworkPolicy

### 对应 JD

第 3 条（整条）、加分项 2

---

## 阶段 3 · 持续交付

**时长**：1.5–2 周 · **前置**：阶段 2

### 任务

1. **镜像流水线** —— CI 构建多架构镜像，推 GHCR，tag 用 `<git-sha>` + 语义化版本
2. **GitOps** —— Argo CD 监听 `deploy/` 目录，集群状态与 Git 自动同步
3. **环境提升** —— dev 自动部署，staging 手动批准
4. **渐进式发布** —— Argo Rollouts 金丝雀：10% → 30% → 100%，每步之间有分析阶段
5. **自动回滚** —— AnalysisTemplate 查询 Prometheus，金丝雀版本错误率或 p95 超标则自动回滚
6. **发布可观测性** —— Grafana 上标注部署事件，能看到"这个延迟上升是从哪次发布开始的"

### 验收

- 提交一个故意引入 500ms 延迟的改动，**金丝雀自动回滚**，全程无人工干预
- 从 push 到 dev 环境生效 < 5 分钟
- 回滚到任意历史版本一条命令完成

### 知识点

- GitOps 的核心：声明式 + Git 为唯一真相源 + 自动收敛（对比传统 push 式部署）
- 金丝雀 / 蓝绿 / 滚动的适用场景与代价
- 数据库迁移和滚动发布的冲突：**为什么 schema 变更必须向后兼容**（expand-contract 模式）
- 镜像 tag 策略：为什么 `latest` 在生产是灾难
- 部署事件与指标的关联

### 对应 JD

第 2 条（版本发布）、第 4 条（CI/CD）

---

## 阶段 4 · SRE 方法论落地

**时长**：1 周 · **前置**：阶段 1 + 3

前面阶段建的是工具，这个阶段建的是**方法**。工作量最小，但面试区分度最高。

### 任务

1. **重定义 SLI** —— 明确好事件与总事件的定义，区分可用性和延迟 SLI（承接 [D-02](../tasks/D-02-error-rate-sli-server-errors-only.md) 的修复）
2. **Error Budget** —— 28 天窗口，Grafana 上展示剩余预算和消耗速率
3. **多窗口多燃烧率告警** —— 替换现有的静态阈值（[D-13](../tasks/D-13-error-budget.md)）

   | 长窗口 | 短窗口 | 燃烧率 | 响应级别 |
   |---|---|---|---|
   | 1h | 5m | 14.4× | page |
   | 6h | 30m | 6× | page |
   | 3d | 6h | 1× | ticket |

4. **Runbook** —— 每条告警一份，`runbook_url` 填进 annotations（[D-14](../tasks/D-14-alert-runbooks.md)）
5. **Alertmanager 路由** —— 分级路由、抑制规则（`ServiceDown` 触发时抑制该服务的 `HighErrorRate`）、静默机制、接一个真实通知渠道（[D-12](../tasks/D-12-alertmanager-receiver.md)）
6. **容量规划** —— 阶梯压测找拐点（[D-16](../tasks/D-16-capacity-load-testing.md)），产出容量模型文档：X QPS 需要 Y 副本 Z 资源

### 验收

- Error budget 看板能回答"本月还能容忍多久的故障"
- 短促尖峰不触发 page，缓慢燃烧能触发 ticket
- 每条告警都能点进 runbook
- 容量文档能回答"要撑 1000 QPS 需要什么配置"，且有压测数据支撑

### 知识点

- SLI / SLO / SLA 的区别，以及为什么内部 SLO 要严于对外 SLA
- Error budget 作为**开发速度与稳定性之间的仲裁机制**（预算充足就快速发布，预算耗尽就冻结功能只修稳定性）
- 燃烧率的数学：为什么是 14.4 倍（30 天预算 / 2 天 = 14.4）
- 多窗口的作用：长窗口抑制误报，短窗口保证及时消音
- 告警疲劳的成因与治理
- 容量规划：利特尔法则、排队论直觉、为什么利用率超过 70% 后延迟会非线性恶化

### 对应 JD

第 6 条（稳定性优化）

---

## 阶段 5 · 混沌工程与故障复盘

**时长**：2 周 · **前置**：阶段 1 + 2 + 4

**面试价值最高的阶段。** 前面所有建设在这里被检验 —— 你会发现很多"以为配好了"的东西其实没生效。

### 场景库

在 `chaos/` 下，每个场景一个目录，包含注入脚本、预期行为、观测记录。

| # | 场景 | 注入手段 | 检验什么 | 关联缺陷 |
|---|---|---|---|---|
| 1 | Redis master 宕机 | 删 Pod | Sentinel 选举耗时、客户端重连、期间丢多少请求 | [D-04](../tasks/D-04-sentinel-quorum-ha.md) |
| 2 | flight-service 完全不可用 | 缩容到 0 | 熔断器打开、503 快速失败而非超时堆积 | [D-01](../tasks/D-01-circuit-breaker-error-classification.md) |
| 3 | 下游高延迟 | toxiproxy 注入 500ms | 超时传播、连接池耗尽、上游雪崩 | — |
| 4 | PG 连接池耗尽 | 打满连接 | 排队 vs 失败、连接池参数影响 | — |
| 5 | Pod OOMKilled | 压低 memory limit | 重启行为、QoS 驱逐顺序、有无数据丢失 | [D-10](../tasks/D-10-container-resource-limits.md) |
| 6 | 节点驱逐 | `kubectl drain` | PDB 是否生效、优雅停机是否真的工作 | [D-03](../tasks/D-03-graceful-shutdown.md) |
| 7 | DNS 故障 | 破坏 CoreDNS | K8s 服务发现路径、客户端 DNS 缓存行为 | — |
| 8 | 磁盘写满 | 填充 PVC | PG 只读降级、告警是否覆盖 | — |
| 9 | **库存泄漏** | 在 ReserveSeats 成功后 kill booking-service | 暴露 [D-06](../tasks/D-06-seat-inventory-leak.md)，然后设计并验证修复方案 | **[D-06](../tasks/D-06-seat-inventory-leak.md)** |
| 10 | 网络分区 | NetworkPolicy 切断 | 分区两侧各自的行为 | — |

### 每个场景的执行流程

```
1. 写下假设      —— 我预期会发生什么，哪条告警会响，MTTR 多少
2. 注入故障      —— 记录精确时间
3. 观测          —— 实际发生了什么，实际哪条告警响了（或没响）
4. 恢复          —— 记录耗时
5. 对比          —— 假设 vs 现实，差异在哪，为什么
6. 写复盘        —— docs/reports/postmortems/YYYY-MM-DD-<事件>.md
7. 落实行动项    —— 改代码 / 改配置 / 改告警 / 补 runbook
8. 重新演练      —— 用数据证明改进有效
```

**第 5 步的"差异"是最有价值的产出。** "我以为熔断器会在 5 秒内打开，实际用了 47 秒，因为窗口计数逻辑有个边界问题" —— 这种话面试官一听就知道你真做过。

### 验收

- 至少 6 个场景完成完整流程并有复盘文档
- 至少 2 个场景发现了**未预期的问题**并修复
- 有改进前后的量化对比（MTTR、影响请求数）

### 知识点

- 混沌工程的原则：先在非生产验证、有明确假设、控制爆炸半径、能随时中止
- MTTR 的分解：检测时间 + 定位时间 + 修复时间，各自的优化手段不同
- 故障的常见放大模式：重试风暴、雪崩、惊群、缓存击穿
- Blameless postmortem 文化：为什么追责会让人隐瞒故障，从而让组织失去学习机会

### 对应 JD

第 6 条（故障响应、问题复盘）—— **这是 JD 里最难通过刷题准备的一条**

---

## 阶段 6 · 运维自动化 / vibe coding

**时长**：1–1.5 周 · **前置**：阶段 4 + 5

**必须放在最后。** 自动化的前提是流程已经清晰且被验证过 —— 自动化一个混乱的流程只会得到自动化的混乱。

前面五个阶段积累的 runbook 和复盘，正好是自动化的输入：**runbook 里那些每次都一样的手动步骤，就是该被自动化的东西。**

### 任务

1. **巡检自动化** —— 定时任务检查证书有效期、PVC 使用率、副本健康、备份完整性、告警规则语法，产出日报
2. **一键诊断工具** —— 输入时间窗，自动抓取该窗口的指标快照、相关日志、K8s 事件、当时的部署记录，打包成诊断报告
3. **告警自愈** —— 特定告警触发自动处置（例：磁盘 85% → 自动清理旧日志并通报）
4. **AI 辅助工作流**
   - 项目级 `CLAUDE.md`：构建命令、代码约定、常见陷阱、目录说明（注意先处理 `.gitignore` 里对 `CLAUDE.md` 的忽略，见 [conventions/engineering.md § 10](../conventions/engineering.md)）
   - 复盘草稿生成：喂入故障时间窗的指标和日志，产出复盘初稿供人工修订
   - Runbook 一致性检查：runbook 里的命令是否还有效（引用的资源名/端口是否已变更）
   - 新告警规则的 PR 检查：是否带 runbook_url、表达式是否会误报

### 验收

- 巡检任务每天自动跑并产出可读报告
- 诊断工具能把一次故障的排查准备时间从 10 分钟压到 1 分钟
- 至少一个告警实现了闭环自愈并演练验证过

### 边界（重要）

自动化必须有安全边界：

- **自愈动作只能是幂等且可逆的**。删除、扩缩容、回滚这类高风险操作需要人工确认
- **一切自动动作必须留痕**，能回答"是谁/什么在什么时候改了什么"
- **AI 生成的运维脚本必须人工审阅**，尤其是任何有写权限的操作
- 自动化失败时要**降级到人工**，不能静默失败

### 对应 JD

第 4 条（自动化运维平台、脚本开发减少重复劳动）

---

## 里程碑与产出

| 阶段完成时 | 简历上能写什么 |
|---|---|
| 0 + 1 | 完整可观测性体系（指标/日志/链路三支柱联查），修复 4 个生产级缺陷 |
| 2 | K8s 化部署，滚动更新零停机，HPA 弹性伸缩 |
| 3 | GitOps 持续交付，金丝雀发布 + 基于 SLI 的自动回滚 |
| 4 | SLO 体系与 error budget，多窗口燃烧率告警，容量模型 |
| 5 | **10 个故障场景演练 + 复盘文档，MTTR 从 X 降到 Y** |
| 6 | 自动化巡检与告警自愈 |

阶段 5 是整条路线里最有分量的一项。前面四个阶段都是"我搭了 X"，阶段 5 是"我把它打坏过 10 次，每次都搞清楚了为什么"。

---

## 需要同时补的基础（贯穿全程）

这些不占用独立阶段，但需要持续投入（对应 JD 任职要求 3、4 和职责第 7 条）：

- **Linux 排查**：每次故障演练时强制用命令行定位，不许直接看 Grafana。`top` / `vmstat` / `iostat` / `ss` / `lsof` / `strace` / `perf`
- **网络**：在阶段 2 之后用 tcpdump 抓一次 gRPC 调用，看 HTTP/2 帧结构；抓一次 TLS 握手；在 K8s 里追踪一次 DNS 解析的完整路径
- **PostgreSQL**：开慢查询日志、对现有查询做 `EXPLAIN ANALYZE`、观察连接池行为、做一次主从复制配置
- **Redis**：`SLOWLOG` 分析、内存淘汰策略实验、RDB 与 AOF 的恢复演练

建议方式：**每个阶段结束时，用当前系统做 1–2 个基础技能的专项练习**，写成短文档放 [`docs/knowledge/`](../knowledge/) 下。

---

## 现在的下一步

阶段 0 的第一个任务：**两个服务加优雅停机**（[D-03](../tasks/D-03-graceful-shutdown.md)）。

理由：它是阶段 2 的硬前置，改动量小（每个服务 30 行左右），验证方式直观（滚动重启期间压测看零错误），而且背后的知识点（SIGTERM、连接排空、K8s 终止生命周期）是运维面试的高频题。
