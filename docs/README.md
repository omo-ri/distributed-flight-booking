# distributed-flight-booking 工程文档

这个目录是仓库的**工程化文档区**，与面向课程交付的 README（俄语）分开维护。

> 规则（协作边界、编码规范、文档纪律、Git 规范）全部在根目录 [`CLAUDE.md`](../CLAUDE.md)，那里是唯一权威。
> `docs/` 装的是**内容**：系统事实、规则的理由、计划、知识、操作手册、历史记录。

## 为什么有这个目录

这个项目起初是一门课程面向服务的架构的第三次作业。

从 2026 年 8 月起定位发生了变化：**它不再只是一次作业交付，而是从原 `coa-hw` 作业仓库中独立出来（保留 Git 历史），作为一个长期演进的"被运维对象"**，用来系统性地建设和练习运维 / SRE 能力 —— 容器编排、发布与回滚、可观测性、故障演练与复盘、容量规划、自动化。

业务代码（订票系统）本身已经足够复杂，具备了真实系统才有的性质：跨服务调用、共享状态、缓存一致性、事务边界、失败传播。这些正是运维体系需要面对的东西。所以路线是**不重写业务，而是在业务周围长出运维体系**。

## 怎么找东西

文档按 **「什么时候需要改它」** 分类，不按主题。分类表见 [`CLAUDE.md`](../CLAUDE.md) § 3，理由见 [conventions/documentation.md](./conventions/documentation.md)。

| 我想…… | 去这里 |
|---|---|
| 知道该怎么干活（规则） | [`../CLAUDE.md`](../CLAUDE.md) |
| 搞清楚这个系统到底怎么跑的 | [architecture/overview.md](./architecture/overview.md) |
| 查表结构和约束 | [architecture/data-model.md](./architecture/data-model.md) |
| 查接口、错误码映射 | [architecture/contracts.md](./architecture/contracts.md) |
| 盘点已有能力（面试前回顾） | [architecture/capabilities.md](./architecture/capabilities.md) |
| 知道某条规则**为什么**是这样 | [conventions/](./conventions/) |
| 决定下一步做什么 | [tasks/README.md](./tasks/README.md) |
| 看长期规划 | [plans/roadmap.md](./plans/roadmap.md) |
| 告警响了怎么办 | [runbooks/](./runbooks/) |
| 查历史故障和压测数据 | [reports/](./reports/) |
| 复习某个技术主题 | [knowledge/](./knowledge/) |

## 当前状态

- 作业评分维度（1–10 分）：**全部完成**
- 工程维度：处在"开发得很好但没被运维过"的位置
- 已识别缺陷：17 条（16 待办 + 1 已完成），见 [tasks/README.md](./tasks/README.md)
- 下一步：阶段 0 的 [D-03 优雅停机](./tasks/D-03-graceful-shutdown.md)

## 相关仓库

同一门课的作业 2（`hw2-marketplace`，单体 REST API）仍留在原 [`coa-hw`](https://github.com/omo-ri/coa-hw) 仓库，作为参考实现（OpenAPI 代码生成、接口/实现分离、JWT + RBAC、状态机建模），不纳入本仓库的演进范围。
