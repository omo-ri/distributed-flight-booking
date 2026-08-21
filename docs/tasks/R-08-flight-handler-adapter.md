---
id: R-08
title: flight-service handler 改成入站适配器（含 D-19 / D-20 的 flight 侧）
severity: major
status: todo
phase: foundation
blocks: []
refs:
  - flight-service/internal/handler/flight.go
  - docs/tasks/D-20-internal-error-disclosure.md
---

# R-08 flight-service handler 改成入站适配器（含 D-19 / D-20 的 flight 侧）

**位置**：`flight-service/internal/handler/flight.go` —— 越级匹配在 `:48, :67, :86`，内部错误外泄在 `:32, :52, :74, :90`。

## 现象 / 触发场景

**两个病灶在同一批代码里。**

**① 入口层跨过业务层，直接匹配数据库层的错误：**

```go
row, err := h.svc.GetFlight(ctx, req.GetId())
if errors.Is(err, repository.ErrNotFound) {          // :48
    return nil, status.Error(codes.NotFound, "flight not found")
}
```

flight 的 service 一个错误都没定义，于是 handler 只能越级去认 repository 的。对照 booking——它 service 定义 sentinel、handler 用 `errors.Is` 映射，是全项目错误处理最干净的地方。**同一个仓库里两套做法。**

**② 内部错误原文当成 gRPC 消息返给调用方：**

```go
return nil, status.Errorf(codes.Internal, "search flights: %v", err)   // :32, :52, :74, :90
```

数据库连接串、表名、约束名，全都随着错误消息出去了。这是已登记的 [D-20](./D-20-internal-error-disclosure.md)，本条把它的 flight 侧一次修完。

## 根因

翻译发生的位置不对。依赖方向定了之后，正确形态是推导出来的、没有选择余地：

```
        入站适配器                                    出站适配器
  (handler: HTTP / gRPC)                    (repository / cache / grpcclient)

  domain error ──▶ 状态码 + 泛化消息          驱动错误 / 传输错误 ──▶ domain error
       ▲                                                    │
       └────────────── service 只见 domain error ◀──────────┘
```

[R-07](./R-07-flight-repository-adapter.md) 做完了右半边，本条做左半边。

## 修法

**1. 新增 `handler/errors.go`**，把散在四个方法里的错误分支收成一张映射表：领域错误 → gRPC 码 + **泛化消息**。

错误是**跨层契约**，散在实现里就没法一眼看全。顺带解决拆分问题——`handler/flight.go` 现在 148 行装了处理函数 + 错误映射 + pb 翻译三件事，拆完剩下的就讲得清了。

**2. 内部错误只进日志，不进响应**：`logctx.Fail(ctx, err)` 记 wrap 链，对外统一 `codes.Internal` + `"internal error"`。调用方需要定位时报 `trace_id`——[D-11](./done/D-11-structured-logging.md) 已经把 trace_id 跨服务贯通了，这条路是通的。

**3. `InsufficientSeatsError` 用 `errors.As` 取出可用座位数**，挂进汇总行（`reason=insufficient_seats`、`available=N`）。`available` 字段先进 [`conventions/engineering.md`](../conventions/engineering.md) 的白名单表再加常量。

**4. handler 不再 `import repository`**——这是本条最硬的一条验收。

**与 [D-19](./D-19-grpc-http-status-mapping.md) 的关系**：D-19 修的是 booking 侧「收到 gRPC 码之后映射成什么 HTTP 码」，本条修的是 flight 侧「吐出什么 gRPC 码」。**本条是 D-19 的数据源头**——源头稳定了，D-19 才有确定的输入可映射。D-19 本身归 [R-12](./R-12-booking-grpcclient-adapter.md)。

## 验收标准

- **怎么验证它生效了**：`grep -n "repository" internal/handler/*.go` 零命中
- 单测表驱动：每个领域错误 → 期望的 gRPC 码，且**响应消息里不含任何内部细节**（断言消息属于一个固定的泛化字符串集合）
- 单测：注入一个含 `password=` / 表名的数据库错误，断言它出现在日志、**不出现在**响应里
- 单测：座位不足时汇总行有 `reason=insufficient_seats` 与 `available`
- pytest 全绿——L3 只测契约一致性（真实服务实际吐什么错误码），本条正好是它的断言对象
- **怎么知道它出问题了**：D-20 的复现手法（构造一个数据库错误，看响应）
- **怎么回滚**：单个 commit revert；对外只是错误消息变泛化，错误码不变

## 学到什么

**「入口层直接认数据库错误」不是偷懒，是被逼的**——中间那层没有定义任何错误，跨层是唯一能编译过的写法。坏味道的成因常常在**别的地方缺了东西**，而不是在出味道的这一行。

在这里显形的还有一条：**同一个仓库里两个服务对同一件事有两套做法时，先看健康的那个为什么健康。** booking 干净是因为它的 service 定义了 sentinel；flight 脏是因为它没有。差别不在写代码的人有多细心。
