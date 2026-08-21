---
id: R-09
title: flight-service 建立 ports.go，缓存端口化并换掉 nil 判断
severity: major
status: todo
phase: foundation
blocks: [T-09]
refs:
  - flight-service/internal/service/flight.go
  - flight-service/internal/cache/redis.go
  - docs/tasks/T-08-cache-interface.md
---

# R-09 flight-service 建立 ports.go，缓存端口化并换掉 nil 判断

**位置**：`flight-service/internal/service/flight.go:19-26`（依赖具体类型）、`:30, :35, :51, :62, :65, :81, :116`（七处 nil 判断）、`:38, :52, :68, :82`（`cache.MarshalJSON` / `UnmarshalJSON` 出现在业务逻辑里）。

**本条取代 [T-08](./T-08-cache-interface.md)**——方向一致，但 T-08 的描述是按旧结构写的（「cache 抽接口」没说接口归谁定义、返回什么类型）。

## 为什么需要

```go
type flightService struct {
    repo  *repository.FlightRepo
    cache *cache.RedisCache // nil = caching disabled
}
```

依赖的是**具体类型**，而「没配缓存」用 `nil` 表达。后果有三层：

1. `if s.cache == nil` / `!= nil` 在四个方法里出现了**七次**——每加一个用例就要再抄一次，漏一次就是 nil panic
2. `cache.MarshalJSON` 直接出现在业务逻辑里——**缓存的序列化格式泄漏进了业务规则**
3. 想给 service 写 L1 单测就必须起 Redis，因为没有别的东西能填进那个字段

第三条是准入规则「**能在上一档证明的事，不许放到下一档**」（`CLAUDE.md` § 4）当前守不住的直接原因。

## 做什么

**1. 新增 `service/ports.go`** —— 这个包的目录：

```go
// 对外提供什么
type FlightService interface { ... }

// 需要什么工具
type FlightRepo interface { ... }
type FlightCache interface { ... }
```

**接口写在用它的那一层，不写在实现它的那一层。** 这是整套设计里最容易写反的一条：Go 的惯例是「接受接口、返回结构体」，而接口的定义权属于**消费方**——只有消费方知道自己需要什么。`repository` / `cache` 包不该定义这些接口，它们只是碰巧满足。

`ports.go` 二十来行，讲清楚这一层的全部输入输出，是写新代码时**唯一需要先读的文件**。加一个用例先看它知道手上有什么工具；改一个依赖先改它再改实现。

**2. 端口按 domain 类型说话，不按 `[]byte` 说话**：`FlightCache` 的方法收发 `domain.Flight`，序列化由 cache 适配器自己决定。service 从此见不到 `json.Marshal`。

**3. 七处 nil 判断换成 `nopCache`**——一个所有方法都空转、读永远 miss 的实现。没配 Redis 时装它进去，业务代码里一处 nil 判断都不剩。

`logctx` 里 `cache=bypass` 这个取值要保留（`logctx.CacheBypass`）：由 `nopCache` 自己挂，语义不变。

**4. 判据提醒：不是所有依赖都要倒置。** `logctx` 不倒置（任何环境下都是同一个 context 字段袋），`uuid.New()` 不倒置。给不会被替换的东西抽接口，付出的是真实成本（每层多一个字段、组装多一根线、读代码多一次跳转），换来的是零。**「到处是抽象但看不懂」的代码就是这么来的。**

## 验收标准

- **怎么验证它生效了**：`grep -n "cache == nil\|cache != nil" internal/service/*.go` 零命中；`grep -n "MarshalJSON\|UnmarshalJSON" internal/service/*.go` 零命中
- `service/ports.go` 存在，且 `flightService` 结构体的字段类型全是本包定义的接口
- **typed-nil 地雷有断言保护**：把一个值为 nil 的 `*cache.RedisCache` 赋给接口变量，接口本身**不是 nil**——这是 Go 里最经典的一个坑，装配点必须显式选 `nopCache` 而不是塞一个 nil 指针进去。写一条测试把它钉死
- 单测：`nopCache` 下走完四个用例，每次都回落到 repository，且汇总行 `cache=bypass`
- pytest 全绿 + `docker compose up` 行为不变（有 Redis / 无 Redis 两种配置各起一次）
- **怎么回滚**：单个 commit revert

## 注意事项

做完这条，[T-09](./T-09-in-memory-cache-fake.md) 的描述要跟着改：fake 要实现**service 定义的端口**，不是实现 `cache` 包的类型。改完之后 flight 的 service 层断言可以从 L2 降到 L1。

## 学到什么

**`nil` 是一个偷偷摸摸的第二实现。** 「没配缓存」是一个完全正当的运行形态，但用 nil 表达它，就等于要求每一个调用点都记得处理这个形态——而这个要求是隐式的，编译器不提醒，漏了就是 panic。**给「什么都不做」一个名字**（`nopCache`），这个形态就从「每处都要记得」变成「装配时选一次」。

`ports.go` 解决的是另一个问题：**打开一个包，第一眼看到什么。** 现在第一眼看到的是 `SearchFlights` 的实现细节；想知道这层能干什么，得从 134 行里挑出四个方法签名。**没有任何一个二十行的文件能回答「我手上有什么工具」——这才是「不知道从哪开始写」的直接来源。**
