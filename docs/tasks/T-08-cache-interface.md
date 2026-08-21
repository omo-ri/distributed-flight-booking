---
id: T-08
title: service 层缓存抽象为接口，nil 判断换成 nopCache
severity: major
status: todo
phase: foundation
blocks: [T-09]
refs:
  - flight-service/internal/service/flight.go
  - flight-service/internal/cache/redis.go
  - flight-service/cmd/main.go
---

# T-08 service 层缓存抽象为接口，nil 判断换成 nopCache

**位置**：`flight-service/internal/service/flight.go:20,23`

## 为什么需要

L2 要覆盖缓存路径，但现在做不到——参数是具体类型：

```go
type flightService struct {
	repo  *repository.FlightRepo
	cache *cache.RedisCache   // nil = caching disabled
}

func NewFlightService(repo *repository.FlightRepo, c *cache.RedisCache) FlightService
```

**不存在"塞一个内存假实现"这个选项。** 而 `service/flight.go` 里有 6 处缓存读写加一整个 `invalidateFlightCache`（`:105-121`），[`design/system-design.md`](../design/system-design.md) 第 6 章一整章在讲缓存一致性——这条路径当前一行都不会被测到。

## 做什么

**本条只做到"编译器能验证"为止**，内存假实现归 [T-09](./T-09-in-memory-cache-fake.md)。

1. 在**消费方**（`service` 包）定义接口，六个方法：`GetFlight` / `SetFlight` / `InvalidateFlight` / `GetSearch` / `SetSearch` / `InvalidateSearchByFlight`。`MarshalJSON` / `UnmarshalJSON` 是包级函数，不进接口
2. `*cache.RedisCache` 保持为唯一实现（此时行为零变化）
3. **加 `nopCache`——六个方法全空操作**，装配时没配 Redis 就用它
4. **删掉全部 `s.cache != nil` 判断**（`:29, 45, 56, 72, 107` 五处）

## 注意事项

> **这是本条真正的风险，不处理就会踩上。**

```go
// flight-service/cmd/main.go:50
var redisCache *cache.RedisCache      // 没配 Redis 时保持 nil
...
svc := service.NewFlightService(repo, redisCache)
```

参数一旦换成接口，**`s.cache != nil` 会永远为真**——一个 nil 的 `*cache.RedisCache` 赋给接口变量，接口本身非 nil（有类型无值）。后果是没配 Redis 时第一个请求走到 `s.cache.GetFlight(...)` 直接 nil 指针 panic。

而 `docker-compose.yml` 里 flight-service 配了 Sentinel，**本地和 CI 都不会触发**——只有"没配 Redis 就起服务"那条路径会炸，恰好是 [D-22](./D-22-redis-startup-not-degradable.md) 关心的那条。

**这就是为什么第 3、4 步必须一起做**：用 `nopCache` 兜底把 nil 判断整个消灭掉，比留着 nil 判断再小心翼翼地处理 typed-nil 更安全，代码也更干净。

## 验收标准

- `go build ./...` 通过
- **手工验收一条**：`REDIS_SENTINEL_ADDR= REDIS_ADDR= go run ./cmd` 起服务，打一次 `GetFlight`，**不 panic**
- `docker compose up` 起来后缓存行为与改动前一致（pytest 全绿）
- `grep -n 'cache != nil' flight-service/` 无匹配

## 学到什么

**Go 的 typed-nil 陷阱在"具体类型换接口"这个重构里是默认结果，不是意外。** 只要原代码用 `!= nil` 表达"没有这个依赖"，换接口就一定会踩上——而且踩上之后编译通过、测试全绿，只在特定部署配置下炸。

正确的解法不是"小心处理 nil"，是**用一个什么都不做的实现替代"没有实现"**。空对象模式在这里不是设计洁癖：它把一个运行时分支变成了装配时的一次选择，分支消失了，出错的地方也就消失了。
