# 代码结构落地计划

> [`design/code-structure.md`](../design/code-structure.md) 第七章「落地路径」的排期形态。
> 设计**为什么**是这样，去看那份文档；这里只回答**按什么顺序做、每一步做完算什么**。
> 全部完成后归档本文件。

## 范围

第 0 批（五条独立改动）+ 第 1 步（`T-01`）+ 第 2 步（flight 倒置）+ 第 3 步（booking 照搬），共 **14 步**。

**第 4 步（Loki + Alloy 入栈）不在本计划内**——它的硬前置是 [D-09](../tasks/D-09-prometheus-persistence.md)（Prometheus 持久化 + 15 天保留），且与本计划的代码改动互不依赖。等这 14 步走完再单开一份。

## ID 序列

新开 **`R-xx`** 序列：这批改动既不是「已有代码的具体缺陷」（`D-xx`），也不是「测试基础设施」（`T-xx`）或「JD 能力缺口」（`G-xx`）——它是**结构重构**，判据是「读一条业务规则需要同时理解几种技术细节」。

`R-01` ~ `R-05` 严格说更接近 `D-xx`（都是审查现有代码审出来的），但它们和后面八条同属一次连贯的落地，放在一个序列里才看得出因果。

## 执行顺序

**一次一条，每条独立提 PR。** 每条的验收标准写在各自卡片里。

| # | ID | 做什么 | 阻塞于 |
|---|---|---|---|
| 1 | [R-01](../tasks/R-01-logctx-semantic-wrappers.md) | `logctx` 语义化封装 + 按字段的写入语义 | — |
| 2 | [R-02](../tasks/R-02-flight-grpc-recovery.md) | flight gRPC recovery 拦截器 + `stack` + `panics_total` | R-01 |
| 3 | [R-03](../tasks/R-03-booking-panic-stack.md) | booking panic 堆栈进汇总行 + `panics_total` | R-01 |
| 4 | [R-04](../tasks/R-04-trace-id-validation.md) | trace_id 32-hex 校验（两侧） | — |
| 5 | [R-05](../tasks/R-05-component-state-log-level.md) | 组件状态行一律 `Warn`（**改 `CLAUDE.md`，单独 PR**） | — |
| 6 | [T-01](../tasks/T-01-extract-app-package.md) | 启动组装抽进 `internal/app`（两个服务） | — |
| 7 | [R-06](../tasks/R-06-flight-domain-package.md) | flight `domain` 包 + 领域错误 | T-01 |
| 8 | [R-07](../tasks/R-07-flight-repository-adapter.md) | flight repository 改出站适配器 | R-06 |
| 9 | [R-08](../tasks/R-08-flight-handler-adapter.md) | flight handler 改入站适配器（D-20 flight 侧） | R-07 |
| 10 | [R-09](../tasks/R-09-flight-service-ports.md) | flight `ports.go` + 缓存端口化 + `nopCache`（取代 T-08） | R-07 |
| 11 | [R-10](../tasks/R-10-booking-domain-package.md) | booking `domain` 包 + sentinel 迁出 | R-09 |
| 12 | [R-11](../tasks/R-11-booking-repository-adapter.md) | booking repository 改出站适配器 | R-10 |
| 13 | [R-12](../tasks/R-12-booking-grpcclient-adapter.md) | booking grpcclient 改出站适配器（含 D-19） | R-10 |
| 14 | [R-13](../tasks/R-13-booking-service-ports.md) | booking `ports.go` + `handler/errors.go`（D-20 booking 侧） | R-11, R-12 |

```
R-01 ──┬──► R-02 flight recovery
       └──► R-03 booking 堆栈
R-04 trace_id 校验     （独立）
R-05 组件状态行 Warn   （独立，改 CLAUDE.md）

T-01 app 抽取 ──► R-06 flight domain ──► R-07 flight repo ──┬──► R-08 flight handler
                                                            └──► R-09 flight ports ──► R-10 booking domain
                                                                                          │
                                    ┌─────────────────────────────────────────────────────┤
                                    ├──► R-11 booking repo ────────┐
                                    └──► R-12 booking grpcclient ──┴──► R-13 booking ports
```

## 三条排期理由

**为什么 R-01 排在 R-02 前面，尽管 R-02 更急。** flight 侧现在一次 panic 就是进程崩溃，优先级确实最高。但 R-01 是小改动（每模块一个文件 + 五处调用点），先落地之后 recovery 拦截器直接用最终 API 写，不返工；反过来做要多改一次。代价是崩溃缺口多存在一天——**这个取舍是显式做的，不是忘了**。

**为什么 T-01 是第 2 步的前置。** 它把两个服务的组装从 `main()` 抽进 `internal/app`，失败路径从 `os.Exit` 改成返回 error。**这一步不碰分层，纯搬家，风险最低**——但它换来的是「测试能在进程内起真实服务」。在它之前，后面每一次重构你都只能靠手工 curl 验证。

**为什么 flight 先、booking 后。** flight 更小（service 134 行 / handler 148 行），而且问题最集中——handler 跨层匹配 `repository.ErrNotFound`、cache 的 nil 判断重复七次、无 recovery、service 返回 `repository.FlightRow`，四条全在这一个服务里。

更核心的理由是：**第一次做这种重构，纸面上的设计落到代码里一定会撞到没想到的东西。** 用较小的服务当试验田，撞到了改设计的成本很低；两个服务一起推进，撞到了就是两倍返工。

## 每一步的回归网

第 2、3 步的每一条都在改生产代码，而 L2 测试骨架（[T-02](../tasks/T-02-testcontainers-harness.md) ~ [T-04](../tasks/T-04-booking-l2-tests.md)）尚未落成。**在它建成之前，pytest 是唯一的回归网**——它走真实 HTTP / gRPC + 直连双库，对内部结构一无所知，只看外部行为。

所以第 2、3 步的每一条都有同一条硬约束：**纯搬运，不掺任何「顺便改一下」。** 一旦掺进去，pytest 全绿就不再能证明什么。

例外是三条**明确列进卡片范围**的缺陷修复（[D-19](../tasks/D-19-grpc-http-status-mapping.md) / [D-20](../tasks/D-20-internal-error-disclosure.md) 两侧），它们的修法本身就是「在适配器边界统一翻译」，和重构是同一个动作，分开做反而要改两次。

## 顺带解决的已登记任务

| 任务 | 归到哪一步 | 变成什么 |
|---|---|---|
| [T-08](../tasks/T-08-cache-interface.md) | R-09 **取代** | 接口由 service 定义、收发 domain 类型、nil 换 `nopCache` |
| [T-09](../tasks/T-09-in-memory-cache-fake.md) | R-09 之后**重写** | fake 实现 service 定义的端口，不是实现 `cache` 包的类型 |
| [D-19](../tasks/D-19-grpc-http-status-mapping.md) | R-12（源头在 R-08） | 两段各自完整的映射，不是逐个 handler 打补丁 |
| [D-20](../tasks/D-20-internal-error-disclosure.md) | R-08（flight）+ R-13（booking） | 在入站适配器里统一翻译 |
| [T-03](../tasks/T-03-flight-l2-tests.md) / [T-04](../tasks/T-04-booking-l2-tests.md) | R-13 之后**重写** | 一批断言从 L2 降到 L1；L2 保留真实 SQL 与并发事务 |

## 做完之后还欠的

- **Loki + Alloy 入栈**（第 4 步）——前置 [D-09](../tasks/D-09-prometheus-persistence.md)
- **in-flight gauge + 告警**——「一次请求一条汇总行」在请求结束前不存在，这个盲区不该用日志补（[`design/code-structure.md`](../design/code-structure.md) § 五）。它的另一半是 [D-25](../tasks/D-25-no-timeouts-anywhere.md)：有了超时，盲区长度 = 超时时长
- **`rate(panics_total[5m]) > 0` 告警**——R-02 / R-03 把指标加上了，告警规则还没写
