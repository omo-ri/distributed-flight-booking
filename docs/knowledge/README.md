# 知识库

按主题组织的技术笔记，**与本仓库解耦** —— 这里的内容离开这个项目也应该成立。

## 与其他文档的分工

| 问题 | 去哪 |
|---|---|
| 这个项目的 Redis 是怎么配的？ | [`architecture/overview.md`](../architecture/overview.md) |
| Sentinel 的 quorum 该设多少、为什么？ | 本目录 `datastore/` |
| 这个项目的 Sentinel 配错了要怎么修？ | [`tasks/D-04`](../tasks/D-04-sentinel-quorum-ha.md) |

**判据**：如果一段内容里出现了 `booking-service` 这类本仓库专有名词，它多半不属于这里。

## 目录

| 主题 | 覆盖 | 关联阶段 |
|---|---|---|
| `kubernetes/` | Pod 生命周期、探针、QoS 与驱逐、Service 与 kube-proxy、CoreDNS、StatefulSet、调度、网络模型 | 阶段 2 |
| `observability/` | 指标基数、直方图与分位数、OTel Context 传播、采样策略、Loki vs ELK、LogQL | 阶段 1 |
| `sre/` | SLI/SLO/SLA、error budget、燃烧率、多窗口告警、告警疲劳、容量规划、MTTR 分解、blameless 复盘 | 阶段 4 |
| `linux-network/` | tcpdump 与 HTTP/2 帧、TLS 握手、`ss` 与连接状态、DNS 解析路径、`top`/`vmstat`/`iostat`/`strace`/`perf`、火焰图 | 贯穿全程 |
| `datastore/` | PostgreSQL：慢查询、`EXPLAIN ANALYZE`、连接池、vacuum 与膨胀、主从复制；Redis：淘汰策略、大 key/热 key、RDB vs AOF、`SLOWLOG`、Sentinel/Cluster | 贯穿全程 |

## 写作方式

按 [roadmap.md](../plans/roadmap.md) 的建议：**每个阶段结束时，用当前系统做 1–2 个基础技能的专项练习，写成短文档放进来。**

即：知识笔记的来源是**在这个系统上真做过一次**，不是读文档做摘抄。抄来的东西记不住，也讲不出来。

每篇建议包含：

```markdown
# 主题

## 问题是什么          为什么会有这个机制，不解决会怎样
## 机制怎么工作        原理
## 我在什么场景验证的   具体命令 + 观测到的输出（可以引用 reports/ 里的记录）
## 常见误区            尤其是"看起来对但其实错"的做法
## 面试怎么讲          压缩成 3–5 句
```

最后一节不是应试技巧 —— **能把一个机制压缩到三句话讲清楚，是真懂了的标志。**
