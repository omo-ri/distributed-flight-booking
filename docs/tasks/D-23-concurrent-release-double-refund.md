---
id: D-23
title: 并发取消同一订单会重复归还座位
severity: critical
status: todo
phase: 0
blocks: []
refs:
  - flight-service/internal/repository/flight.go
  - booking-service/internal/service/booking.go
---

# D-23 并发取消同一订单会重复归还座位

**位置**：`flight-service/internal/repository/flight.go:140-170`（`ReleaseReservation` 的事务体）

## 现象 / 触发场景

释放路径的事务里，第一步查预留记录**没有加 `FOR UPDATE`**：

```go
err := tx.QueryRow(ctx,
    `SELECT id, flight_id, booking_id, seat_count, status, created_at
     FROM seat_reservations WHERE booking_id = $1 AND status = 'ACTIVE'`, bookingID,   // :142-145 ← 无锁
).Scan(...)
...
_, err = tx.Exec(ctx,
    `UPDATE flights SET available_seats = available_seats + $1 WHERE id = $2`,          // :154-156
    res.SeatCount, res.FlightID)
_, err = tx.Exec(ctx,
    `UPDATE seat_reservations SET status = 'RELEASED' WHERE id = $1`, res.ID)          // :162-163
```

PostgreSQL 默认隔离级别是 READ COMMITTED，普通 `SELECT` 不加锁、不阻塞。所以两个并发的取消请求：

```
事务 A                              事务 B
SELECT ... status='ACTIVE'  → 命中   SELECT ... status='ACTIVE'  → 也命中（无锁，不阻塞）
UPDATE flights SET +2               （等 flights 行锁）
UPDATE reservations → RELEASED
COMMIT                              UPDATE flights SET +2   ← 又加了一次
                                    UPDATE reservations → RELEASED（同一行，无害）
                                    COMMIT
⇒ available_seats 多出 2，凭空造出座位
```

`UPDATE flights` 各自会取得行锁，所以两次加法是**串行执行的、都成功**——行锁保证了原子性，但**保证不了"只该执行一次"**。

**上游的检查挡不住**：`booking-service/internal/service/booking.go:211` 的 `if b.Status == "CANCELLED"` 读 `bookings` 时同样没加锁，两个并发请求都会通过这一关。

**数据库约束只能部分兜底**：`chk_available_le_total`（`available_seats <= total_seats`）只在航班本来就接近坐满时才会拦下多出来的座位。**只要航班没坐满，多出来的座位就静默写进数据库**，没有任何报错、日志或指标。

**触发条件很普通**：用户在"取消"按钮上双击、客户端超时后重发取消请求、或者两个标签页同时操作。不需要任何极端场景。

## 根因

对比同一个文件里的 `ReserveSeats`（`:81-131`）——它面对完全相同的并发问题，但是安全的，靠的是两层：

1. `SELECT available_seats ... FOR UPDATE`（`:99-101`）把同航班的事务串行化
2. `INSERT seat_reservations` 撞上 `booking_id UNIQUE` 时**整个事务回滚**，连同扣减一起撤销

**释放路径两层都没有**：没有 `FOR UPDATE`，而且它只做 `UPDATE`——`UPDATE ... SET status='RELEASED'` 对一条已经是 `RELEASED` 的行执行时**不会报错**，只是影响 0 行。所以没有任何东西能把"这次操作已经做过了"变成一个可回滚的失败。

扣减路径的幂等是"约束 + 事务回滚"给的，而这个组合在释放路径上不存在。**幂等不是自动继承的，每条写路径都要各自成立。**

## 修法

**推荐做法**——把状态迁移提到事务最前面，用受影响行数做判据：

```go
tag, err := tx.Exec(ctx,
    `UPDATE seat_reservations SET status = 'RELEASED'
     WHERE booking_id = $1 AND status = 'ACTIVE'`, bookingID)
if tag.RowsAffected() == 0 {
    return ErrNotFound   // 已经被别人释放过了，或本来就不存在
}
// 只有真正完成迁移的那个事务才继续加座位
```

`UPDATE ... WHERE status='ACTIVE'` 是一次**原子的状态迁移**：并发的两个事务里只有一个能让 `RowsAffected() == 1`，另一个拿到 0。这把幂等性建在状态机上，而不是建在锁上。

中途失败时和加座位在同一个事务里，一起回滚，安全。

**备选做法**：第一个 `SELECT` 加 `FOR UPDATE`。也正确，但会增加持锁窗口，而且它是"靠锁保证"而不是"靠状态机保证"——前者更脆弱，将来有人改动语句顺序就可能失效。

**注意**：`ErrNotFound` 会映射成 gRPC `NOT_FOUND`，而上游 `CancelBooking` 当前会把这个错误**静默吞掉**（`service/booking.go:218-223`，见 [D-24](./D-24-booking-idempotency-key.md) 之外的另一条，`docs/design/system-design.md` § 5.4 问题 2）。修完本条后"重复取消"会走进那条吞错误的路径——行为上正确（不重复加座位），但仍然对用户显示"取消成功"。这是可接受的，只是要知道两条路径会在这里交汇。

## 验收标准

- 集成测试：对同一个已确认订单**并发**发起 20 次 `POST /bookings/{id}/cancel`，结束后 `available_seats` 只增加一次预留的座位数
- 集成测试：串行发起两次取消，第二次返回 409（`ErrAlreadyCancelled`）或 200，但 `available_seats` 不再变化
- 单元/仓储层测试：`ReleaseReservation` 对同一个 `booking_id` 调用两次，第二次返回 `ErrNotFound` 且 `flights.available_seats` 不变
- 回归：正常的单次取消仍然正确归还座位（现有 E2E 用例覆盖）

## 学到什么

**"有约束保护"和"这条路径有约束保护"是两回事。** `booking_id UNIQUE` 让扣减幂等，很容易让人以为整个预留生命周期都幂等了——但唯一约束只在 `INSERT` 时起作用，释放路径根本不 `INSERT`。

一般化的判据：**幂等要靠"重复执行会失败"来实现，而 `UPDATE` 天生不会失败。** 想让 `UPDATE` 具备幂等性，必须把前置状态写进 `WHERE` 子句并检查 `RowsAffected`——这是状态机式幂等的标准形态，和唯一约束式幂等是两套不同的工具。

还有一条：**这个缺陷的方向和 [D-06](./D-06-seat-inventory-leak.md) 相反**（D-06 是座位漏掉，本条是座位凭空多出来），但成因完全不同，不能指望修 D-06 顺带修掉它。库存正确性需要**每条读写路径分别论证**，不能整体地"感觉安全"。
