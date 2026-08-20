---
id: T-05
title: 四档分层落成：Makefile 入口、CI 拓扑、镜像复用、标签守卫
severity: major
status: todo
phase: foundation
blocks: []
refs:
  - .github/workflows/ci.yml
  - Makefile
  - booking-service/Dockerfile
  - flight-service/Dockerfile
---

# T-05 四档分层落成：Makefile 入口、CI 拓扑、镜像复用、标签守卫

**前置**：[T-02](./T-02-testcontainers-harness.md)、[T-03](./T-03-flight-l2-tests.md)、[T-04](./T-04-booking-l2-tests.md)（要先有东西可编排）

## 为什么需要

四档的定义在 `CLAUDE.md` § 4，但当前 CI 里没有任何一档的位置：`unit` job 跑 `go test ./...`（`ci.yml:50,54`），加了 L2 之后它会去起容器。

同时 CI 现在有两处浪费：

- **同一套栈被构建两遍**：`ci.yml:74`（integration）和 `ci.yml:153`（load-test）各自 `docker compose up --build -d`，各自从零编译两个 Go 服务，跑在两台 runner 上零复用
- **仓库根没有 `.dockerignore`**，而 `booking-service/Dockerfile` 用 `context: .`（`docker-compose.yml:108`）——整个仓库包括 `.git/`、`docs/`、`k6/out/` 都被打包成构建上下文送进 daemon，Dockerfile 里一行都用不到

## 做什么

**Makefile**：四档各一个 target，本地能单独跑任意一档。

**CI 拓扑**：

| job | 内容 | 依赖 |
|---|---|---|
| `build` | 编译 + `docker compose build` → `docker save` → 上传 artifact | — |
| `unit` | `go test -race ./...`（**不带标签**，不起容器） | — |
| `l2` | `go test -race -tags=integration`，只需 Docker，**不依赖 build 产物** | — |
| `integration` | `docker load` + pytest | build, unit |
| `load-test` | `docker load` + k6（烟雾，**非门禁**） | build, unit |

**PR 选择性执行**（只作用在 `l2` job 上）：

- 按 `go list -deps` 算真实依赖图，**不按目录名匹配**——否则会漏掉所有依赖改动包的下游
- **触发全量的白名单**：`proto/`、`*/migrations/`、`go.mod` / `go.sum`、`docker-compose.yml`、`internal/app/`、workflow 文件本身。这些改动在 Go 依赖图上看不出影响面
- **`main` 分支全量**。选择性执行是加速反馈，不是最终门禁

**标签守卫**：`go test -tags=integration -list '.*' ./...` 的测试数低于阈值就失败。

**`.dockerignore`**：补上，至少排除 `.git/`、`docs/`、`k6/out/`、`metrics-report.json`、`tests/`。

**`scripts/verify_metrics.py` 的 SLO 门禁降级**：理由见 [`conventions/testing.md`](../conventions/testing.md) § 6。

## 验收标准

- 四档能各自独立跑，本地和 CI 用的是同一个入口
- `unit` job **不启动任何容器**
- 故意把某个 L2 文件的标签拼错（`integraton`），守卫能让 CI 红
- 改一个只被 flight-service 依赖的包，PR 上不触发 booking-service 的 L2
- 改 `proto/` 下任意文件，PR 上触发全量
- 镜像只构建一次，`integration` 与 `load-test` 都从 artifact 加载

## 注意事项

**不要把 `integration` 和 `load-test` 合并成一个 job。** 合并能省一次起栈，但一个红叉会同时可能是"契约错了"和"性能退化了"——按分档的准入判据（失败时说明了什么），信号的清晰度不该拿来换几分钟。

**`.dockerignore` 只做排除构建上下文这一件事**，不碰 `runAsNonRoot`——那是 [D-15](./D-15-container-nonroot.md)，改变运行时行为，风险级别不同。

**镜像走 artifact 而不是 GHCR。** 推 registry 是阶段 3 的任务（tag 策略、`packages: write` 权限、fork PR 无推送权限），现在做属于跨阶段跳跃。

**依赖性质会变**：`needs: [build, unit]` 从逻辑门禁变成物理依赖，build 挂了下游是 "download artifact 失败" 而不是 skip。

## 学到什么

**"选择性执行"的风险不在于跑得少，在于漏跑是静默的。** 一个没跑的测试和一个不存在的测试，在 CI 绿灯上长得一模一样。所以每一处"跳过"都要配一个守卫：标签配计数守卫，PR 选择性配全量白名单 + `main` 兜底。**任何优化只要能产生假绿，就必须自带一个能变红的机制。**
