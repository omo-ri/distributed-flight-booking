---
id: D-07
title: 密钥明文写在配置里
severity: major
status: todo
phase: 2
blocks: []
refs:
  []
---

# D-07 密钥明文写在配置里

**位置**：`docker-compose.yml:94, 117` —— `AUTH_API_KEY: "super-secret-key"`，还有各处的 `flight_pass` / `booking_pass`，全部明文提交进 Git。

作业环境无所谓，但迁移到 K8s 时必须解决，而且**密钥一旦进过 Git 历史就等于永久泄漏** —— 改了配置文件，历史提交里还在。

**修法路径**：本地 `.env`（已在 `.gitignore`）→ K8s Secret → 加密的 GitOps（SOPS / Sealed Secrets）。
