---
id: D-03
title: 没有优雅停机
severity: critical
status: todo
phase: 0
blocks: [phase-2]
refs:
  - booking-service/cmd/main.go
  - flight-service/cmd/main.go
---

# D-03 没有优雅停机

**位置**：`booking-service/cmd/main.go`、`flight-service/cmd/main.go` —— 全仓库搜不到任何 `signal.Notify` / `Shutdown` / SIGTERM 处理。

进程收到 SIGTERM 直接死掉。正在处理的请求被切断，数据库连接不归还，gRPC 流被中断。

**当前影响有限**（Compose 手动重启偶尔断几个请求），**但迁移到 K8s 后会变成持续性问题**：每次滚动更新、每次扩缩容、每次节点驱逐、每次 HPA 缩容，都会掉一批正在处理的请求。发布期间错误率飙升，而且查不出原因。

**修法**：两个服务都要
1. `signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)`
2. HTTP 侧 `echo.Shutdown(timeoutCtx)`；gRPC 侧 `srv.GracefulStop()`
3. 关闭顺序：先停止接收新请求 → 等待在途请求完成（带超时上限）→ 关闭 DB / Redis 连接
4. K8s 侧配合 `preStop` sleep 和 `terminationGracePeriodSeconds`

**这是阶段 2 的前置条件**，必须先做。

## 验收标准

- `docker compose stop booking-service` 期间持续压测（k6 常驻负载），**零请求失败**
- 日志里能看到完整关停序列：收到 SIGTERM → 停止接收新请求 → 在途请求排空 → 关闭 DB/Redis 连接 → 进程退出
- 关停有超时上限，在途请求卡死时不会无限等待
- gRPC 侧用 `GracefulStop()` 而非 `Stop()`，验证方式：关停瞬间发起的调用能正常完成
