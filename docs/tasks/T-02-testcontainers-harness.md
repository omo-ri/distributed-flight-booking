---
id: T-02
title: 两个模块各建一套 testcontainers 骨架与 fixture builder
severity: critical
status: todo
phase: foundation
blocks: [T-03, T-04]
refs:
  - flight-service/internal/repository
  - booking-service/internal/repository
  - flight-service/migrations/001_init.up.sql
  - booking-service/migrations/001_init.up.sql
---

# T-02 两个模块各建一套 testcontainers 骨架与 fixture builder

## 为什么需要

L2 要在真实 PostgreSQL 上跑（并发事务、`FOR UPDATE`、约束行为），而现在两个模块里**连一个碰真实 SQL 的测试都没有**——`flight-service/internal/handler/flight_test.go` 用 `mockFlightService` 把 service 层整个替掉，一行 SQL 都不执行。

## 做什么

每个模块各一套（**不能共用**，见下方注意事项）：

1. `TestMain` 起一个 PostgreSQL 容器，**包级共享**（`docker-compose.yml` 用的是 `postgres` 官方镜像，测试沿用同一大版本）
2. **只执行 `001_init.up.sql`**，不执行 seed
3. fixture builder：flight 侧造航班（`total_seats` / `price` / 路线可控），booking 侧造订单
4. 所有 L2 文件带 `//go:build integration` 标签

fixture 的形状按"测试前提可读"来设计：

```go
f := newFlight(t, db, withSeats(1))   // 一行说清"需要一个只剩 1 个座位的航班"
```

## 验收标准

- 两个模块各能在**全新容器**里造出一个航班 / 一个订单并读回
- `go test ./...`（不带标签）**不启动任何容器**
- `go test -tags=integration ./...` 能跑通，且两次连续运行结果一致（无残留状态）
- 同一个包内两个测试并行（`t.Parallel()`）互不干扰——这是 [T-03](./T-03-flight-l2-tests.md) 并发测试成立的前提

## 注意事项

**两套代码不能共用。** Go 的 `internal` 规则跨模块生效，`flight-service` 和 `booking-service` 的测试辅助代码无法互相 import。这是"两个模块各测各的"这个决定买下的代价，在这里显形：同一套做法要写两遍。

**不要为了消除重复去建共享模块。** 那需要把辅助代码提到 `internal` 之外变成公开 API，等于为了测试便利去加深两个服务之间的耦合——而 [`design/system-design.md`](../design/system-design.md) § 2.1 已经记了一笔"契约的物理隔离没有做到"，不该再往里加。

**seed 不进 L2 是有意的**，理由见 [`conventions/testing.md`](../conventions/testing.md) § 3。

## 学到什么

**测试的隔离级别决定了它能测什么。** 共享数据的测试套件永远无法测并发——不是因为写不出来，是因为你无法区分"看到的异常是被测代码的缺陷"还是"另一个测试踩进来了"。而这个系统最需要被验证的性质（超卖 = 0）恰恰只在并发下才存在。
