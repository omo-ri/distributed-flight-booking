# 任务看板

一条任务一个文件。ID 在整个仓库里稳定 —— 提交信息用 `Refs: D-01` 引用它。

- **D-xx** —— 已有代码里的**具体缺陷**，来源是对现有实现的审查
- **G-xx** —— 需要从零建设的**能力**，来源是 [`../plans/gap-analysis.md`](../plans/gap-analysis.md)（当前尚未拆分为任务，随阶段推进逐步拆出）
- **T-xx** —— **测试基础设施**的建设条目。它既不是已有代码的缺陷，也不是 JD 能力缺口，所以单开一个序列。文件用下方模板的变体：`现象 / 根因` 两节换成 `为什么需要 / 做什么`，其余相同

状态流转（`todo` → `doing` → 移入 `done/` 并同步 `architecture/capabilities.md`）的规则见 [`CLAUDE.md`](../../CLAUDE.md) § 3。**做完不搬文件 = 没做完。**

## 待办

### 地基阶段 · 测试骨架与容量基线

排在阶段 0 之前。理由：阶段 0 起的每一条缺陷修复都要求"先有能变红的测试"，而这套设施当前不存在。分档规则见 [`CLAUDE.md`](../../CLAUDE.md) § 4，理由与空洞清单见 [`../conventions/testing.md`](../conventions/testing.md)。

**完成判据**：四档能各自跑起来，且 [D-19](./D-19-grpc-http-status-mapping.md) 与 [D-23](./D-23-concurrent-release-double-refund.md) 的复现测试**能稳定变红**。修绿归阶段 0——红色证明的是这套设施能持续发现问题，绿色只证明某一次修复。

| ID | 任务 | 级别 | 状态 | 阻塞 / 影响 |
|---|---|---|---|---|
| [T-01](./T-01-extract-app-package.md) | 启动组装抽进 `internal/app` | 🔴 critical | todo | 阻塞 T-02；同时是 D-03 的骨架 |
| [T-02](./T-02-testcontainers-harness.md) | testcontainers 骨架 + fixture builder（两模块各一套） | 🔴 critical | todo | 阻塞 T-03 / T-04 |
| [T-03](./T-03-flight-l2-tests.md) | flight L2：真实 SQL、并发事务、service 层 | 🔴 critical | todo | **D-23 的复现载体** |
| [T-04](./T-04-booking-l2-tests.md) | booking L2：本地 repository + pb 契约桩 | 🔴 critical | todo | **D-19 的复现载体** |
| [T-08](./T-08-cache-interface.md) | 缓存抽接口 + `nopCache` | 🟠 major | todo | 阻塞 T-09；**有 typed-nil 地雷** |
| [T-09](./T-09-in-memory-cache-fake.md) | 缓存的内存假实现 | 🟡 minor | todo | 阻塞 T-03 的 service 层部分 |
| [T-06](./T-06-k6-three-scenarios.md) | k6 改造成三场景，开环压测 | 🟠 major | doing | 阻塞 T-07；写路径拐点已实测，缺 `steady` 与 `read/ladder` 两跑 |
| [T-07](./T-07-local-capacity-baseline.md) | 本机容量基线报告 | 🟠 major | todo | 阻塞 D-27 / D-25 / D-16 |
| [T-05](./T-05-four-tier-ci-topology.md) | 四档落成：Makefile + CI 拓扑 + 镜像复用 + `.dockerignore` | 🟠 major | todo | 需要 T-02/T-03/T-04 先有东西可编排 |

### 阶段 0 · 修复阻塞缺陷

| ID | 任务 | 级别 | 状态 | 阻塞 / 影响 |
|---|---|---|---|---|
| [D-03](./D-03-graceful-shutdown.md) | 没有优雅停机 | 🔴 critical | todo | 阻塞阶段 2 K8s 迁移 |
| [D-01](./D-01-circuit-breaker-error-classification.md) | 熔断器把业务错误计入失败统计 | 🔴 critical | todo | 熔断器可信度 |
| [D-02](./D-02-error-rate-sli-server-errors-only.md) | 错误率 SLI 包含客户端错误 | 🔴 critical | todo | 整个 SLO 体系的正确性 |
| [D-25](./D-25-no-timeouts-anywhere.md) | 整条调用链没有任何超时 | 🔴 critical | todo | **阻塞 D-21**；下游卡死会拖垮上游，熔断器失效 |
| [D-23](./D-23-concurrent-release-double-refund.md) | 并发取消会重复归还座位 | 🔴 critical | todo | 数据正确性：库存凭空增加 |
| [D-24](./D-24-booking-idempotency-key.md) | `POST /bookings` 没有幂等保护 | 🔴 critical | todo | 数据正确性：客户端重发会重复扣库存 |
| [D-05](./D-05-health-check-endpoints.md) | 没有健康检查端点 | 🟠 major | todo | 阻塞阶段 2 K8s 迁移 |
| [D-19](./D-19-grpc-http-status-mapping.md) | gRPC 错误码到 HTTP 的映射不完整 | 🟠 major | todo | D-02 的数据源头，只修 D-02 不干净 |
| [D-20](./D-20-internal-error-disclosure.md) | 内部错误详情返回给外部客户端 | 🟠 major | todo | 信息泄漏；完整修复依赖 D-11 |
| [D-21](./D-21-half-open-no-probe-limit.md) | 熔断器 HALF_OPEN 不限流 | 🟠 major | todo | 下游恢复时惊群；与 D-01 同包同改 |
| [D-22](./D-22-redis-startup-not-degradable.md) | Redis 连不上导致服务无法启动 | 🟠 major | todo | 阶段 2 后会变成 CrashLoopBackOff |
| [D-15](./D-15-container-nonroot.md) | 容器以 root 运行 | 🟡 minor | todo | 容器安全基线 |

### 阶段 1 · 可观测性补全

| ID | 任务 | 级别 | 状态 | 阻塞 / 影响 |
|---|---|---|---|---|
| [D-11](./D-11-structured-logging.md) | 日志不可用于排障 | 🟠 major | todo | 阻塞阶段 5 故障演练 |
| [D-09](./D-09-prometheus-persistence.md) | 监控数据不持久 | 🟡 minor | todo | 跨天趋势、error budget 周期统计 |
| [D-27](./D-27-histogram-buckets-mismatch.md) | 直方图桶与延迟量级不匹配 | 🟡 minor | todo | p95 不可信 → CI 门禁看不见性能退化 |

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
| [D-18](./D-18-openapi-request-validation.md) | OpenAPI 请求校验没有生效 | 🟡 minor | todo | 规范与实现分家，非 UUID 入参返回 500 |
| [D-26](./D-26-price-snapshot-from-cache.md) | 订单金额快照读的是缓存价格 | 🟡 minor | todo | 当前无改价路径，触发不了；加改价功能时立即生效 |
| [D-28](./D-28-missing-down-migrations.md) | 迁移全部只有 up，没有 down | 🟡 minor | todo | `CLAUDE.md` § 4 的规则当前无对象可遵守；阻塞阶段 3 的回滚 |

## 已完成

| ID | 任务 | 级别 | 完成于 |
|---|---|---|---|
| [D-17](./done/D-17-build-artifacts-in-git.md) | 构建产物被提交进 Git | 🔵 trivial | 文档重构（`docs/restructure`） |

## 阻塞关系

```
T-06 k6 三场景 ──► T-07 本机基线 ──┬──► D-27 桶边界（按实测分布定）
                                   ├──► D-25 超时取值（按实测 p99 定）
                                   └──► D-16 容量规划

T-01 app 抽取 ──► T-02 容器骨架 ──┬──► T-03 flight L2 ──► D-23 能变红
                                  └──► T-04 booking L2 ─► D-19 能变红
T-08 缓存接口 ──► T-09 假实现 ─────────► T-03 的 service 层部分
T-02 / T-03 / T-04 ────────────────────► T-05 四档与 CI 落成

D-03 优雅停机 ─┐
               ├──► 阶段 2 Kubernetes 迁移
D-05 健康检查 ─┘

D-11 结构化日志 ──► 阶段 5 故障演练（没有能查的日志，演练出故障只能干瞪眼）
D-11 结构化日志 ──► D-20 的完整修复（对外不回显错误详情，需要 request_id 能跨服务串起来）

D-25 全链路超时 ──► D-21 HALF_OPEN 限流（探测请求不返回，熔断器会卡死在 HALF_OPEN）

（D-25 的超时值与 D-27 的桶边界都必须由实测数据决定，而实测数据由 T-07 产出——见上方第一张图。
  2026-05-29 那次基线被脚本里的 sleep 限住，只测到了 k6 自己的天花板；T-06 已把它换成开环阶梯。）

D-19 错误码映射 ──► D-02 错误率 SLI（D-02 修聚合口径，D-19 修数据源头，只修一头不干净）
```

**同包同改建议**：[D-01](./D-01-circuit-breaker-error-classification.md) 与 [D-21](./D-21-half-open-no-probe-limit.md) 都在 `internal/circuitbreaker`，一次改完；D-01 要加的熔断器指标正好能验证 D-21 的效果。

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
