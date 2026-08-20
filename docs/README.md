# distributed-flight-booking 工程文档

这个目录是仓库的**工程化文档区**，与面向课程交付的 README（俄语）分开维护。

## 为什么有这个目录

这个项目起初是一门课程面向服务的架构的第三次作业。

从 2026 年 8 月起定位发生了变化：**它不再只是一次作业交付，而是从原 `coa-hw` 作业仓库中独立出来（保留 Git 历史），作为一个长期演进的"被运维对象"**，用来系统性地建设和练习运维 / SRE 能力 —— 容器编排、发布与回滚、可观测性、故障演练与复盘、容量规划、自动化。

业务代码（订票系统）本身已经足够复杂，具备了真实系统才有的性质：跨服务调用、共享状态、缓存一致性、事务边界、失败传播。这些正是运维体系需要面对的东西。所以路线是**不重写业务，而是在业务周围长出运维体系**。

## 怎么找东西

文档按 **「什么时候需要改它」** 分类，不按主题。理由见 [conventions/documentation.md](./conventions/documentation.md)。

| 我想…… | 去这里 |
|---|---|
| 搞清楚这个系统到底怎么跑的 | [architecture/overview.md](./architecture/overview.md) |
| 查表结构和约束 | [architecture/data-model.md](./architecture/data-model.md) |
| 查接口、错误码映射 | [architecture/contracts.md](./architecture/contracts.md) |
| 盘点已有能力（面试前回顾） | [architecture/capabilities.md](./architecture/capabilities.md) |
| 动手写代码之前 | [conventions/engineering.md](./conventions/engineering.md) |
| 决定下一步做什么 | [tasks/README.md](./tasks/README.md) |
| 看长期规划 | [plans/roadmap.md](./plans/roadmap.md) |
| 告警响了怎么办 | [runbooks/](./runbooks/) |
| 查历史故障和压测数据 | [reports/](./reports/) |
| 复习某个技术主题 | [knowledge/](./knowledge/) |

## 六类文档

| 类别 | 目录 | 时效性 | 更新触发条件 |
|---|---|---|---|
| **事实** | [`architecture/`](./architecture/) | 永远描述当下 | 代码改了就得改 |
| **规范** | [`conventions/`](./conventions/) | 永远描述当下 | 做法变了才改 |
| **意图** | [`plans/`](./plans/) [`tasks/`](./tasks/) | 描述未来 | 完成即归档 |
| **知识** | [`knowledge/`](./knowledge/) | 与本仓库解耦 | 学到东西就写 |
| **操作** | [`runbooks/`](./runbooks/) | 永远可执行 | 告警或系统变了就改 |
| **记录** | [`reports/`](./reports/) | 冻结在某个时间点 | **永不修改**，只新增 |

## 工作流

一个缺陷从被发现到被消灭，会走完整条链路：

```
plans/gap-analysis.md 识别缺口
      ↓ 拆解
tasks/D-xx.md (todo)
      ↓ 开分支，提交信息带 Refs: D-xx
tasks/done/D-xx.md (done)
      ↓ 同一个 PR 内必须同步
architecture/capabilities.md 加一条（带代码位置）
      ↓ 如果做了故障演练
reports/postmortems/ 出一份，行动项回流成新的 tasks/
```

**任何改变运行时行为的 PR，必须同时动 `tasks/` 和 `architecture/`。**

## 当前状态

- 作业评分维度（1–10 分）：**全部完成**
- 工程维度：处在"开发得很好但没被运维过"的位置
- 已识别缺陷：17 条（16 待办 + 1 已完成），见 [tasks/README.md](./tasks/README.md)
- 下一步：阶段 0 的 [D-03 优雅停机](./tasks/D-03-graceful-shutdown.md)

## 文档约定（摘要）

- **语言**：本目录用中文。根目录的课程交付文档（`README.md`）保持原语言不动 —— 它的受众是课程评分者，不是我们。
- **可验证性**：陈述系统行为时必须给出可核对的依据 —— 代码路径加行号、配置文件位置、或可复现的命令。不写"应该"、"大概"。
- **区分事实与计划**：已经存在的东西写在 `architecture/`，还不存在的写在 `plans/` / `tasks/`。
- **同步责任**：文档漂移比没有文档更有害。

完整规范见 [conventions/documentation.md](./conventions/documentation.md)。

## 仓库结构

```
distributed-flight-booking/
├── docs/                  # 本目录 —— 工程文档
├── proto/                 # gRPC 契约
├── flight-service/        # gRPC 服务
├── booking-service/       # REST 服务
├── prometheus/  grafana/  alertmanager/   # 可观测性栈
├── k6/  tests/  scripts/  # 负载、测试、运维脚本
└── .github/workflows/     # CI
```

同一门课的作业 2（`hw2-marketplace`，单体 REST API）仍留在原 [`coa-hw`](https://github.com/omo-ri/coa-hw) 仓库，作为参考实现（OpenAPI 代码生成、接口/实现分离、JWT + RBAC、状态机建模），不纳入本仓库的演进范围。
