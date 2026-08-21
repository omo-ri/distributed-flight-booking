---
id: R-04
title: trace_id 照单全收外部输入，没有任何校验
severity: major
status: todo
phase: foundation
blocks: []
refs:
  - booking-service/cmd/main.go
  - flight-service/internal/logging/interceptor.go
---

# R-04 trace_id 照单全收外部输入，没有任何校验

**位置**：`booking-service/cmd/main.go:109`（`middleware.RequestID()`）与 `:153` 取值处；`flight-service/internal/logging/interceptor.go:92-103` 的 `traceIDFrom`。

## 现象 / 触发场景

两处都**优先采信外部传入的值**且不做任何校验：booking 采信 `X-Request-ID`，flight 采信 metadata 里的 `x-trace-id`。

先排除一个不成立的担心：**日志伪造不成立**。`slog.NewJSONHandler` 会转义换行，塞 `\n{"level":"error"...}` 进去只会得到一个带转义符的字符串字段。

真实的问题不需要有攻击者：

- **任何一个客户端固定传同一个 `X-Request-ID`，所有请求就共用一个 trace_id**——k6 脚本里手滑写死一个 header 就够了。接了 Loki 之后，点 derived field 想看「这一条请求发生了什么」，拉出来的是几十万条不相干的行，**而且这个失效是静默的**
- **长度不受控**：超大 header 变成超大日志行。Loki 有 `max_line_size` 上限，超了**整行丢弃**——最需要的那条日志就这么没了

## 根因

`trace_id` 是**外部可控输入**，但它被当成内部生成值使用了。透传是对的（跨服务关联靠它），照单全收是错的。

## 修法

两侧各加一道校验，判据一致：**必须是 32 位十六进制**，否则丢弃并自己生成。

```go
func validTraceID(s string) bool {
    if len(s) != 32 {
        return false
    }
    _, err := hex.DecodeString(s)
    return err == nil
}
```

这个格式不是随便挑的——`flight-service/internal/logging/interceptor.go:98-102` 现在自生成的就是 16 字节 hex（= 32 个十六进制字符），而它**正好是 W3C Trace Context 的 trace-id 格式**。将来真上 OpenTelemetry 时换的只是「谁生成它」，字段名和值的形状一个字都不用动（见 [`design/code-structure.md`](../design/code-structure.md) § 五）。

booking 侧要注意：`middleware.RequestID()` 已经把值写进了响应头，校验要在 `logctx.New` 取值那一步（`main.go:153`）之前完成，且**响应头里回给客户端的也应该是校验后的值**——否则客户端拿到的 ID 和日志里的对不上。

**这是 Loki 落地前的硬前置**：不修它，derived field 从第一天起就可能指向一堆噪音。

## 验收标准

- **怎么验证它生效了**：单测表驱动——空串 / 31 位 / 33 位 / 含非 hex 字符 / 8KB 长串，断言全部换成自生成的 32-hex；合法的 32-hex 断言原样透传
- 跨服务贯通不回退：现有的「booking 与 flight 汇总行 trace_id 相同」的断言继续绿
- booking 侧断言响应头 `X-Request-ID` 与汇总行 `trace_id` **始终一致**
- `go test -race ./...` 两个模块都过
- **怎么知道它出问题了**：Loki 上线后，某个 trace_id 的行数远超一次请求应有的条数
- **怎么回滚**：删掉校验函数的调用，退回直接采信

## 学到什么

**「外部传入的优先」是一个安全决策，不只是一个便利决策。** 它在正常使用下完全正确（这正是分布式追踪需要的），只在被误用时失效——而失效方式是「日志还在、级别还在、就是查不出东西」，没有任何一处会报错。

判据其实和指标标签那条同源：**取值集合有约束吗？** 只不过这里约束的不是基数，是格式。
