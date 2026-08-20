# 任务看板

一条任务一个文件。ID 在整个仓库里稳定 —— 提交信息用 `Refs: D-01` 引用它。

- **D-xx** —— 已有代码里的**具体缺陷**，来源是对现有实现的审查
- **G-xx** —— 需要从零建设的**能力**，来源是 [`../plans/gap-analysis.md`](../plans/gap-analysis.md)（当前尚未拆分为任务，随阶段推进逐步拆出）

状态流转（`todo` → `doing` → 移入 `done/` 并同步 `architecture/capabilities.md`）的规则见 [`CLAUDE.md`](../../CLAUDE.md) § 3。**做完不搬文件 = 没做完。**

## 待办

### 阶段 0 · 修复阻塞缺陷

| ID | 任务 | 级别 | 状态 | 阻塞 / 影响 |
|---|---|---|---|---|
| [D-03](./D-03-graceful-shutdown.md) | 没有优雅停机 | 🔴 critical | todo | 阻塞阶段 2 K8s 迁移 |
| [D-01](./D-01-circuit-breaker-error-classification.md) | 熔断器把业务错误计入失败统计 | 🔴 critical | todo | 熔断器可信度 |
| [D-02](./D-02-error-rate-sli-server-errors-only.md) | 错误率 SLI 包含客户端错误 | 🔴 critical | todo | 整个 SLO 体系的正确性 |
| [D-05](./D-05-health-check-endpoints.md) | 没有健康检查端点 | 🟠 major | todo | 阻塞阶段 2 K8s 迁移 |
| [D-15](./D-15-container-nonroot.md) | 容器以 root 运行 | 🟡 minor | todo | 容器安全基线 |

### 阶段 1 · 可观测性补全

| ID | 任务 | 级别 | 状态 | 阻塞 / 影响 |
|---|---|---|---|---|
| [D-11](./D-11-structured-logging.md) | 日志不可用于排障 | 🟠 major | todo | 阻塞阶段 5 故障演练 |
| [D-09](./D-09-prometheus-persistence.md) | 监控数据不持久 | 🟡 minor | todo | 跨天趋势、error budget 周期统计 |

### 阶段 2 · Kubernetes 迁移

| ID | 任务 | 级别 | 状态 | 阻塞 / 影响 |
|---|---|---|---|---|
| [D-04](./D-04-sentinel-quorum-ha.md) | Redis Sentinel 不构成高可用 | 🟠 major | todo | HA 有效性 |
| [D-07](./D-07-plaintext-secrets.md) | 密钥明文写在配置里 | 🟠 major | todo | 阶段 2 前必须解决 |
| [D-10](./D-10-container-resource-limits.md) | 容器没有资源限制 | 🟡 minor | todo | HPA 阈值无依据（依赖 D-16） |

### 阶段 4 · SRE 方法论

| ID | 任务 | 级别 | 状态 | 阻塞 / 影响 |
|---|---|---|---|---|
| [D-13](./D-13-error-budget.md) | 有 SLO 但没有 error budget | 🟠 major | todo | SRE 方法论 |
| [D-14](./D-14-alert-runbooks.md) | 告警没有 runbook | 🟠 major | todo | 可运维性 |
| [D-12](./D-12-alertmanager-receiver.md) | Alertmanager 没有接收端 | 🟡 minor | todo | 告警不触达任何人 |
| [D-16](./D-16-capacity-load-testing.md) | 压测无法回答容量问题 | 🟡 minor | todo | 容量规划无数据 |

### 阶段 5 · 混沌工程

| ID | 任务 | 级别 | 状态 | 阻塞 / 影响 |
|---|---|---|---|---|
| [D-06](./D-06-seat-inventory-leak.md) | 跨库一致性缺口：库存泄漏 | 🟠 major | todo | 数据正确性 |

### Backlog（未排期）

| ID | 任务 | 级别 | 状态 | 阻塞 / 影响 |
|---|---|---|---|---|
| [D-08](./D-08-grpc-mtls.md) | gRPC 明文传输 | 🟡 minor | todo | API Key 可被抓包窃取 |

## 已完成

| ID | 任务 | 级别 | 完成于 |
|---|---|---|---|
| [D-17](./done/D-17-build-artifacts-in-git.md) | 构建产物被提交进 Git | 🔵 trivial | 文档重构（`docs/restructure`） |

## 阻塞关系

```
D-03 优雅停机 ─┐
               ├──► 阶段 2 Kubernetes 迁移
D-05 健康检查 ─┘

D-11 结构化日志 ──► 阶段 5 故障演练（没有能查的日志，演练出故障只能干瞪眼）
```

## 任务文件模板

```markdown
---
id: D-xx
title: 一句话标题，不带级别图标
severity: critical | major | minor | trivial
status: todo | doing | done
phase: 0 | 1 | 2 | 3 | 4 | 5 | 6 | backlog
blocks: [phase-2]          # 这条不做会卡住什么
refs:
  - path/to/file.go        # 相关代码位置
---

# D-xx 标题

**位置**：代码路径加行号

## 现象 / 触发场景
## 根因
## 修法
## 验收标准        ← 认领任务时必须填，答不出来说明还没想清楚
## 学到什么
```

`验收标准` 对应 [`CLAUDE.md`](../../CLAUDE.md) § 4 的变更纪律三问：怎么验证它生效了、怎么知道它出问题了、怎么回滚。理由见 [`../conventions/engineering.md` § 6](../conventions/engineering.md)。
