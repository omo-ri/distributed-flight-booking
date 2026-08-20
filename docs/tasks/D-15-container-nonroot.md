---
id: D-15
title: 容器以 root 运行
severity: minor
status: todo
phase: 0
blocks: []
refs:
  []
---

# D-15 容器以 root 运行

两个 Dockerfile 的运行阶段都没有 `USER`，容器内进程是 root。也没有 `.dockerignore`（构建上下文把 `.git`、测试文件全塞进去了）。

K8s 侧对应 `securityContext`：`runAsNonRoot: true`、`readOnlyRootFilesystem: true`、`allowPrivilegeEscalation: false`、`capabilities.drop: [ALL]`。

## 验收标准

- `docker compose exec booking-service id` 输出非 root UID，flight-service 同样
- 加了 `.dockerignore`，构建上下文里不再包含 `.git`、`tests/`、`docs/`
- 镜像体积对比记录在 PR 描述里
- 全栈重启后所有 pytest 仍然通过（确认非 root 没有破坏文件权限）
