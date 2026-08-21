---
id: R-12
title: booking-service grpcclient 改成出站适配器（含 D-19）
severity: major
status: todo
phase: foundation
blocks: [R-13]
refs:
  - booking-service/internal/grpcclient/flight.go
  - booking-service/internal/service/booking.go
  - docs/tasks/D-19-grpc-http-status-mapping.md
---

# R-12 booking-service grpcclient 改成出站适配器（含 D-19）

**位置**：`booking-service/internal/grpcclient/flight.go`（对外返回 `*pb.*Response`）；`service/booking.go:83, :97, :111-117`（业务层在判 gRPC 状态码）、`:184` 的 `protoToFlightInfo`。

**这是第 3 步里唯一需要动脑的一条**——flight 侧没有对应物。

## 为什么需要

业务层在判传输层的状态码：

```go
if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
    return FlightInfo{}, ErrFlightNotFound
}
```

gRPC 是**传输细节**。今天它是 gRPC，明天换成 HTTP 调用或者消息队列，业务规则一个字都不该变，但这段代码会全线崩掉。`:184` 的 `protoToFlightInfo` 是同一个问题的另一面——protobuf 类型的翻译住在业务层里。

代价现在就在付：想给 `CreateBooking` 写一条 L1 单测，得先造一个 `*pb.GetFlightResponse`，再造一个带正确 `codes` 的 `status.Error`。**业务规则的测试被迫用传输层的词汇写。**

## 做什么

**1. `FlightClient` 对外只说 domain 的语言**：方法返回 `domain.Flight` / `error`，不再返回 `*pb.*Response`。`protoToFlightInfo`（`service/booking.go:184`）搬进 grpcclient 并改名。

**2. gRPC 状态码在这里翻成领域错误**，一张集中的映射表：

| gRPC 码 | 领域错误 |
|---|---|
| `NotFound` | `domain.ErrFlightNotFound` |
| `ResourceExhausted` | `domain.InsufficientSeatsError`（从错误详情取可用座位数） |
| `InvalidArgument` | 领域侧的参数非法错误 |
| 其余 | wrap 后原样上抛，由入口判 5xx |

service 从此不 import `codes` / `status` / `pb`——**这是本条最硬的一条验收**。

**3. 这张表就是 [D-19](./D-19-grpc-http-status-mapping.md) 的落点。** D-19 现在的形态是「gRPC 错误码到 HTTP 的映射不完整」，倒置之后修法变成两段各自完整的映射（gRPC 码 → 领域错误 → HTTP 码），而不是逐个 handler 打补丁。**没有被覆盖的 gRPC 码必须有明确归宿**，不能默默落进 500。

D-19 同时是 [D-02](./D-02-error-rate-sli-server-errors-only.md)（错误率 SLI 含客户端错误）的数据源头——源头把「业务拒绝」和「服务端错误」分清楚了，D-02 的聚合口径才有干净的输入。

**4. 韧性机制原样保留**：重试（`:116-117`）、熔断（`:83, :89`）、`downstream_code` / `retries` / `cb` 三个汇总行字段的挂载位置**一个都不动**。它们本来就属于适配器层，位置是对的。

**5. `flight_test.go`（工作区已有）跟着改**——断言对象从 pb 响应换成 domain 类型。

## 验收标准

- **怎么验证它生效了**：`grep -n "codes\.\|status\.\|pb\." internal/service/*.go` 零命中
- 映射表有表驱动单测，**每一个 flight-service 实际会吐的码都有一行**；未覆盖码走默认分支且有断言
- 汇总行字段不回退：构造重试成功、熔断打开两种场景，断言 `retries` / `degraded=retry_recovered` / `cb=open` / `downstream_code` 仍然出现
- L1 单测能只用 domain 类型给 `CreateBooking` 造桩——这是本条的直接收益，写一条证明它
- pytest 全绿 + `docker compose up` 行为不变
- **怎么知道它出问题了**：某个 gRPC 码开始映射成 500（[D-02](./D-02-error-rate-sli-server-errors-only.md) 的错误率会看得见）
- **怎么回滚**：单个 commit revert

## 注意事项

**跨模块不共享测试代码**（`CLAUDE.md` § 4）：booking 的 L2 测试用 pb 契约桩对接，桩要跟着改成新的适配器边界。契约一致性（真实 flight-service 到底吐什么码）归 L3，**不要在这一条里靠猜**——不确定的码去 [R-08](./R-08-flight-handler-adapter.md) 的映射表查，那是源头。

## 学到什么

**传输层的错误码是一套「别人定义的领域语言」。** `codes.ResourceExhausted` 在 gRPC 规范里的含义是「配额耗尽」，在这个系统里它恰好被用来表达「座位不足」——这个对应关系是**约定**，不是事实。约定就该有一个显式的落点，而不是散在四个 `switch` 里。

散着的时候，改一个约定要找齐所有匹配点，而**「找齐了吗」这个问题没有可靠答案**。
