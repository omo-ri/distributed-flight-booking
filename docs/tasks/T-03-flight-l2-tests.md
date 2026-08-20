---
id: T-03
title: flight-service L2：真实 SQL、并发事务、service 层
severity: critical
status: todo
phase: foundation
blocks: [T-05]
refs:
  - flight-service/internal/repository/flight.go
  - flight-service/internal/service/flight.go
---

# T-03 flight-service L2：真实 SQL、并发事务、service 层

**前置**：[T-02](./T-02-testcontainers-harness.md)（容器骨架）、[T-09](./T-09-in-memory-cache-fake.md)（假 cache，否则 service 层测试要按 `nil` 写一遍再返工）

## 为什么需要

`flight-service` 是库存的唯一仲裁者，R1（超卖 = 0）这条硬要求的全部实现都在 `repository/flight.go` 的那个事务里。而这段代码**当前零覆盖**——`handler/flight_test.go` 把 service 整个 mock 掉了。

## 做什么

**repository 层（真实 PG）**：

- `ReserveSeats` / `ReleaseReservation` 的正常路径与边界（座位刚好够、差一个、航班不存在）
- **并发路径**：N 个 goroutine 同时预留同一航班，断言 `available_seats` 与 `SUM(seat_reservations.seat_count)` 一致、且从不为负
- 幂等：同一 `booking_id` 重复预留只扣一次（`booking_id UNIQUE` 是这个保证的物理载体）
- 约束行为：`CHECK (available_seats >= 0)` 和 `chk_available_le_total` 各被触发一次

**service 层（真实 PG + 内存假 cache）**：

- 缓存命中时不打库、未命中时回源并回填
- `ReserveSeats` / `ReleaseReservation` 之后 `invalidateFlightCache` 确实被调用，且搜索缓存也被失效

## 验收标准

- **[D-23](./D-23-concurrent-release-double-refund.md) 的复现测试能稳定变红**——并发取消同一订单，座位被重复归还。这是本条最重要的产出：它证明这套骨架能抓到真实缺陷
- 上述测试在**当前未修复的代码上必须红，且多次运行都红**（偶尔红的并发测试等于没有测试）
- 修复 D-23 本身**不属于本条**——地基阶段的完成判据是"能变红"，修绿归阶段 0

## 注意事项

**并发测试要能稳定复现，不能靠碰运气。** 靠 `go func` 一起冲然后指望撞上竞态，是不可重复的。需要用同步原语把并发方对齐到临界点之前（`sync.WaitGroup` 起跑线 + 必要时在事务边界处设置屏障），让竞态窗口每次都被命中。

**每个用例用自己的航班行。** 共享航班会让并发用例互相污染，届时你分不清红是缺陷还是干扰。

## 学到什么

**"测试变红"本身就是交付物。** 一条能稳定复现缺陷的测试，比修复本身更难写、也更有价值——修复没有它就无法验证，回归没有它就无法防止。所以地基阶段的验收停在红色，而不是绿色：绿色证明的是某次修复，红色证明的是这套设施能持续发现问题。
