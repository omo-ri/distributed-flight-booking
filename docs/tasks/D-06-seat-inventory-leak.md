---
id: D-06
title: 跨库一致性缺口：库存泄漏
severity: major
status: todo
phase: 5
blocks: []
refs:
  - booking-service/internal/service/booking.go
---

# D-06 跨库一致性缺口：库存泄漏

**位置**：`booking-service/internal/service/booking.go` 的创建流程

```
1. ReserveSeats(...)   → 成功，flight-db 扣了座位
2. INSERT bookings     → 失败（booking-db 不可用 / 进程被 kill）
   ⇒ 座位被永久扣除，但没有任何订单记录指向它
```

`seat_reservations` 里留下一条 `ACTIVE` 记录，`available_seats` 少了，没有任何机制会释放它。泄漏是**累积的** —— 每次失败漏一点，最终航班显示满座但实际没人订。

**当前没有任何补偿机制**：没有预留超时、没有对账任务、没有 saga 补偿。

**这是分布式系统的经典问题，有多种解法，各有代价**：

| 方案 | 做法 | 代价 |
|---|---|---|
| 预留超时（推荐先做） | `seat_reservations` 加 `expires_at`，未在 N 分钟内被订单确认则自动 `EXPIRED` 并归还座位 | 需要后台清理任务；需要"确认"这一步 |
| Saga 补偿 | 步骤 3 失败时显式调 `ReleaseReservation` 回滚 | 补偿调用本身也会失败，只是把问题概率降低 |
| 对账任务 | 定时比对两库，找出无主预留 | 有延迟；需要跨库读权限 |
| Outbox 模式 | 订单和事件写在同一事务，异步投递 | 复杂度最高，需要消息队列 |

**学习价值极高**：这是"为什么分布式事务难"的活教材，而且能做出可观测的演示 —— 在步骤 2、3 之间注入故障，看着 `available_seats` 一点点漏掉，然后引入超时机制修复它。
