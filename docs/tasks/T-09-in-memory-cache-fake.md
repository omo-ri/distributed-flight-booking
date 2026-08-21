---
id: T-09
title: 缓存的内存假实现，供 L2 使用
severity: minor
status: todo
phase: foundation
blocks: [T-03]
refs:
  - flight-service/internal/service/flight.go
---

# T-09 缓存的内存假实现，供 L2 使用

**前置**：[T-08](./T-08-cache-interface.md)（接口存在之后才有东西可实现）

## 为什么需要

[T-08](./T-08-cache-interface.md) 把缓存抽成了接口，但真正让缓存路径可测的是这一步。拆成两条的理由是**把无回归网的窗口压到最小**：T-08 的改动大部分由编译器验证，本条则是纯新增代码，不碰任何生产路径。

## 做什么

一个内存 map 实现，满足 `service` 包定义的缓存接口。除了存取，它要能被**断言**——L2 需要验证的不是"缓存能存东西"，而是**调用时序**：

- `GetFlight` 未命中 → 回源打库 → 回填；第二次命中 → **不打库**
- `ReserveSeats` / `ReleaseReservation` 之后，航班键和对应的搜索键**都**被失效（`invalidateFlightCache` 会先反查航班拿路由信息，`:114-121`）

所以假实现需要暴露调用记录（哪些键被读过、写过、删过），而不只是一个 map。

## 验收标准

- [T-03](./T-03-flight-l2-tests.md) 的 service 层测试能用它验证上述两条时序
- 假实现**不进生产二进制**——放在带 `//go:build integration` 标签的文件里，或放在 `_test.go` 中

## 注意事项

**假实现不模拟 Redis 的真实行为**：不模拟 TTL 真实过期、不模拟连接中断、不模拟 Sentinel 切换。它验证的是"业务代码有没有在正确的时机调用缓存"，不是"Redis 靠不靠谱"。

这个边界必须写清楚，否则会产生虚假的安全感。已登记在 [`conventions/testing.md`](../conventions/testing.md) § 8 的空洞表里。

## 学到什么

**假实现要伪装的是契约，不是实现。** 一个假 Redis 如果去模拟 TTL 精确过期、模拟网络抖动，它就变成了一个更差的 Redis——维护成本高，而且它的 bug 会伪装成被测代码的 bug。

假实现的价值全部来自**可观测性**：它能告诉你"被测代码用什么参数、按什么顺序调用了依赖"。这件事真实依赖做不到，而这恰恰是单元/集成层最需要验证的东西。
