---
id: T-01
title: 把启动组装从 main() 抽进 internal/app
severity: critical
status: todo
phase: foundation
blocks: [T-02, D-03]
refs:
  - flight-service/cmd/main.go
  - booking-service/cmd/main.go
---

# T-01 把启动组装从 main() 抽进 internal/app

**位置**：`flight-service/cmd/main.go:26-105`（约 60 行）、`booking-service/cmd/main.go:27-112`（约 85 行）

## 为什么需要

测试要在进程内起真实 server（见 [`conventions/testing.md`](../conventions/testing.md) § 4），但现在**没有任何入口能拿到组装好的对象**。三个具体障碍：

| 障碍 | 位置 | 后果 |
|---|---|---|
| 组装 100% 内联在 `main()` 里 | 两个 `main.go` | 测试无从调用 |
| 失败路径全是 `log.Fatalf` / `os.Exit(1)` | flight `:39,45,55,61,79,103`；booking `:46,54,76,110` | **照抄这段逻辑到测试里会直接杀死测试进程**，一条断言都跑不到 |
| 迁移用相对路径 `file://migrations` | flight `main.go:108`、booking 同构 | Go 测试的工作目录是**包目录**，这条路径必然失效 |

第二条决定了"不动生产代码、测试里自己写一份组装"这条路走不通——不是慢，是不可行。

**它同时是 [D-03](./D-03-graceful-shutdown.md) 的骨架。** 优雅停机要求把启动和关闭拆成可控的两阶段，而现在 `main()` 的最后一行是 `srv.Serve(lis)`（flight `:102`）/ `e.Start()`（booking `:108`）直接阻塞，没有任何地方能接住 SIGTERM。做 T-01 顺手就把 D-03 的结构做完了；反过来先修 D-03 也必然要做 T-01 的大部分。

## 做什么

每个服务新增 `internal/app`：

- `app.New(cfg Config) (*App, error)` —— 负责全部组装，**返回 error 而不是 exit**
- `app.Run(ctx) error` / `app.Shutdown(ctx) error` —— 启停分离
- `app.Config` —— 集中所有配置读取，**含迁移目录路径**（参数化，不再写死 `file://migrations`）
- `main.go` 退化成：读环境变量 → `New` → `Run` → 处理错误

顺带满足 `CLAUDE.md` § 4 的「配置读取集中在启动阶段，业务代码里不出现 `os.Getenv`」——现在 flight `main.go:51,58,75` 是 `os.Getenv` 和 `envOrDefault` 混着用。

## 验收标准

- **pytest 全绿**（`make test`）。这是本条唯一的验收网：它走真实 HTTP + 直连双库断言，对内部结构一无所知，只看外部行为
- `docker compose up` 起得来，两个服务的行为与改动前**完全一致**
- `go build ./...` 两个模块都过

## 注意事项

**这次重构必须是纯搬运。** 不改任何行为、不顺手修任何缺陷、不调整中间件顺序（booking `main.go:91-101` 那四个的顺序原样保留）。一旦掺进"顺便改一下"，pytest 全绿就不再能证明什么——而这是在回归网建成之前动生产代码，除了 pytest 你没有别的保护。

`app.Run` 里的启停语义先保持和现在一致（阻塞直到出错）。真正的优雅停机（信号处理、连接排空、`GracefulStop` vs `Stop`）属于 [D-03](./D-03-graceful-shutdown.md)，不在本条范围。

## 学到什么

**"为了可测性而重构"和"为了正确性而重构"经常是同一次改动。** 这里抽 `app` 的直接动机是测试，但它同时是优雅停机的前提——因为两者要的是同一件事：**让"启动"成为一个可以被调用、可以失败、可以撤销的操作**，而不是一个进程的副作用。

`log.Fatalf` 的代价也在这里显形：它把"错误处理"和"进程退出"焊死在一起，于是任何想复用这段逻辑的调用方都被拒之门外。**库代码返回 error，只有 `main` 有资格决定退出。**
