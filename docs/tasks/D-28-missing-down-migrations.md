---
id: D-28
title: 迁移全部只有 up，没有任何 down 文件
severity: minor
status: todo
phase: backlog
blocks: []
refs:
  - flight-service/migrations/001_init.up.sql
  - flight-service/migrations/002_seed.up.sql
  - booking-service/migrations/001_init.up.sql
---

# D-28 迁移全部只有 up，没有任何 down 文件

**位置**：`flight-service/migrations/`（`001_init.up.sql`、`002_seed.up.sql`）、`booking-service/migrations/`（`001_init.up.sql`）

## 现象 / 触发场景

两个目录里**一个 `.down.sql` 都不存在**，而 `CLAUDE.md` § 4 明写「迁移的 up/down 都要能跑」。

两个服务都用 `golang-migrate`（`flight-service/cmd/main.go:108`、booking 同构），这个库本身支持 down，只是文件没写。后果：

- **`migrate down` 不可用**。一次错误的 schema 变更只能靠重建库回退，而重建库意味着**丢数据**
- 阶段 3 的渐进式发布会撞上它：金丝雀回滚时如果 schema 已经变了，没有 down 就回不去（roadmap 阶段 3 知识点里"为什么 schema 变更必须向后兼容"讲的正是这个问题的另一面）
- `CLAUDE.md` 里那条规则**当前无法被执行**——不是没人遵守，是没有对象可遵守

## 根因

初始迁移是项目起步时一次性写的，当时只需要"能建起来"。`golang-migrate` 不强制 down 文件存在，缺失时静默跳过，所以这个空缺从未被任何工具或流程提醒过。

[T-02](./T-02-testcontainers-harness.md) 之后它会**更难被发现**：L2 永远用全新容器、只跑 up，down 从此彻底失去被执行的机会。

## 修法

1. 给三个迁移各补一个 `.down.sql`。`001_init` 的 down 是 `DROP TABLE`；`002_seed` 的 down 是按 id `DELETE`（**不能 `TRUNCATE`**——那会连非 seed 数据一起删掉）
2. 加一个 up → down → up 的往返测试。它天然属于 L2（需要真实 PG，见 `CLAUDE.md` § 4 的分档）

## 验收标准

- 三个 `.down.sql` 存在
- 在一个装有数据的库上 `migrate down` 一步再 `up` 回来，schema 与原状一致
- `002_seed` 的 down 只删三条 seed 记录，手工插入的航班不受影响
- 往返测试进 L2 并在 CI 里跑

## 学到什么

**一条无法被执行的规则，和没有这条规则是两回事——它更坏。** 它会让人以为这件事已经被考虑过了。`CLAUDE.md` 里"迁移的 up/down 都要能跑"这句话存在了很久，而它指向的对象一直不存在，这个矛盾没有被任何检查发现。

**规则要配一个能让它变红的机制。** 这条规则的机制就是那个往返测试——在它存在之前，规则只是一句愿望。
