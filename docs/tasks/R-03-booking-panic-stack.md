---
id: R-03
title: booking-service 的 panic 堆栈是非 JSON 孤儿行
severity: major
status: todo
phase: foundation
blocks: []
refs:
  - booking-service/cmd/main.go
  - booking-service/internal/metrics/metrics.go
---

# R-03 booking-service 的 panic 堆栈是非 JSON 孤儿行

**位置**：`booking-service/cmd/main.go:109-115` —— 中间件注册顺序是 `RequestID → requestLogger → Recover`。

## 现象 / 触发场景

先说**对的部分**：Echo 里注册顺序即执行顺序，所以 `Recover` 在 `requestLogger` **内层**。panic 被 recover → `next(c)` 返回 error → 汇总行照打、`status=500` → `main.go:165-167` 判成 `Error`。这条链路是通的，进程也不会崩。

坏的是**堆栈不在那一行里**。Echo 的 `middleware.Recover()` 用它自己的 logger（非 slog、非 JSON），堆栈被打到另一路输出。于是排障时手里有两样东西：一条说「500 了」但没说为什么的 JSON 汇总行，和一坨不知道属于哪条请求的文本。

接了 Loki 之后这条更难受：那是**非 JSON 的多行文本**，`| json` 解析不了、`level` 标签提不出来——它会变成一堆没有标签、无法关联 `trace_id` 的孤儿行。而 `{level="error"}` 查不到它。

## 根因

`middleware.Recover()` 的默认配置自带日志输出，而这个输出面**不在 `logctx` 口径之内**。「一次请求一条汇总行」这个口径覆盖了我们自己写的每一层，唯独漏了框架自带的这一处。

## 修法

配置 `middleware.RecoverWithConfig`，关掉它自己的打印，改成：

- `logctx.Fail(ctx, err)` + `logctx.Add(ctx, logctx.KeyStack, stack)`，让堆栈进那一条汇总行（`stack` 字段先进 [`conventions/engineering.md`](../conventions/engineering.md) 的白名单表，再加常量）
- `panics_total` counter，与 [R-02](./R-02-flight-grpc-recovery.md) 的 flight 侧同名同义
- **不动中间件顺序**——现在的顺序是对的

## 验收标准

- **怎么验证它生效了**：单测注册一个必定 panic 的 handler，断言 ① 响应是 500 且 body 里不含 panic 原文 ② 输出里**只有一条** JSON 行，带 `level=ERROR`、`trace_id`、`stack` ③ `panics_total` +1
- 断言输出中**不存在非 JSON 行**——这条是本任务的核心验收，`| json` 能解析全部输出才算做完
- `go test -race ./...` 过
- **怎么知道它出问题了**：Loki 里 `{service="booking-service"} != "{"` 应该恒为空
- **怎么回滚**：改回 `middleware.Recover()`

## 学到什么

**「日志口径」的边界不是你写的代码的边界，是进程 stdout 的边界。** 只要有一个第三方组件绕过 slog 直接往 stdout 写，整套「一套解析规则覆盖全部输出」的假设就破了——而这个破法是静默的，本地看 `docker logs` 一切正常，直到接了采集器才发现有一半行没有标签。

同一个教训在 `flight-service/cmd/main.go:43` 已经交过一次学费（go-redis 默认往标准库 log 写，要 `redis.SetLogger` 接管）。**每引入一个会说话的依赖，就要问一次它往哪说。**
