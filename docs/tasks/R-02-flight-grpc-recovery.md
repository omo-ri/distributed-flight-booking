---
id: R-02
title: flight-service 没有 panic recovery，一次 panic 就是进程崩溃
severity: critical
status: todo
phase: foundation
blocks: []
refs:
  - flight-service/cmd/main.go
  - flight-service/internal/logging/interceptor.go
  - flight-service/internal/metrics/metrics.go
---

# R-02 flight-service 没有 panic recovery，一次 panic 就是进程崩溃

**位置**：`flight-service/cmd/main.go:127-133` —— 拦截器链是 `logging → metrics → auth`，**没有 recovery**。

## 现象 / 触发场景

grpc-go 默认不 recover handler 里的 panic。所以 flight-service 里任何一次 panic：

1. **整个进程崩溃**——不是这一条 RPC 失败，是全部在途请求一起没
2. **那条 RPC 的汇总行永远不会写**——`logging/interceptor.go:36` 的 `handler(ctx, req)` 返回之后才走到写日志的代码，panic 直接从那里穿过去了

在 Loki 里看到的是：流量正常 → 突然全断 → 容器重启后恢复，**中间什么都没有**。最需要现场的那一刻，恰恰是日志唯一完全空白的那一刻。

对照 booking 侧：`cmd/main.go:115` 有 `middleware.Recover()`，所以它只是堆栈落点不对（[R-03](./R-03-booking-panic-stack.md)），不会崩进程。两边严重程度差一个量级。

## 根因

拦截器链在设计时只想了「谁需要看到谁」（logging 最外层、auth 最内层），没想「谁需要接住谁」。recovery 是一条**独立的**约束：它必须在**能被记录的范围之内**、在**所有可能 panic 的代码之外**。

## 修法

新增 `internal/recovery` 拦截器，插在 **logging 之内、metrics 之外**：

```go
grpc.ChainUnaryInterceptor(
    logging.UnaryServerInterceptor(log),
    recovery.UnaryServerInterceptor(),   // ← 这里
    metrics.UnaryServerInterceptor(),
    auth.UnaryInterceptor(apiKey),
)
```

顺序的两条边界都是硬的：**在 logging 外面**汇总行丢失（recovery 把 panic 转成 error 之前，日志拦截器根本没机会跑完）；**再往里挪**则 metrics / auth 自己的 panic 抓不到。

拦截器做三件事：

1. `recover()` 之后转成 `status.Error(codes.Internal, "internal error")`——对外只回泛化消息
2. `logctx.Fail(ctx, panicErr)` + `logctx.Add(ctx, logctx.KeyStack, stack)`，由 logging 拦截器按 `Internal` 判成 `Error`。`stack` 字段先进 [`conventions/engineering.md`](../conventions/engineering.md) § 汇总行字段白名单那张表，再加常量
3. `panics_total` counter（`flight-service/internal/metrics/metrics.go`）——没有指标的机制等于不存在

`stack` 遵循「非常态才出现」原则，和 `cb`、`retries` 同类：字段一出现就意味着这条 RPC 走了非常态分支。

## 验收标准

- **怎么验证它生效了**：单测注册一个必定 panic 的 handler，断言 ① 测试进程没死 ② 返回码是 `Internal` ③ 对外消息里不含 panic 原文 ④ 汇总行 `level=ERROR` 且带 `stack` 与 `trace_id` ⑤ `panics_total` +1
- 拦截器顺序有一条断言测试保护——注册顺序写错时它要变红，而不是靠人记
- 汇总行是**单行 JSON**：堆栈里的换行必须被 `slog` 转义，不能把一条日志裂成几十行
- `go test -race ./...` 过
- **怎么知道它出问题了**：`panics_total` 有任何增长；`rate(panics_total[5m]) > 0` 值得成为一条告警（本条不做，记进 backlog）
- **怎么回滚**：从 `ChainUnaryInterceptor` 里摘掉一行

## 学到什么

**「默认行为」是要去查的，不是靠类比猜的。** Echo 有 `Recover` 中间件所以「服务器框架都会兜住 panic」——grpc-go 不会。两个服务在同一个仓库里对同一件事有完全不同的默认值，而这个差异在代码里**看不见**：flight 的 main.go 不会因为缺一个拦截器而长得可疑。

**缺失的东西不会在 diff 里出现，只会在事故里出现。**
