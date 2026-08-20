---
id: D-17
title: 构建产物被提交进 Git
severity: trivial
status: done
phase: 0
blocks: []
refs:
  - k6/out/summary.json
  - metrics-report.json
---

# D-17 构建产物被提交进 Git

`git ls-files` 显示 `metrics-report.json` 和 `k6/out/summary.json` 是被跟踪的，但 `.gitignore` 里写了 `k6/out/`（对已跟踪文件无效）。

这两个是运行时产物，应该只作为 CI artifact 存在。

## 验收标准

- `git ls-files | grep -E 'metrics-report.json|k6/out'` 无输出
- CI 的 load-test job 仍然把这两个文件作为 artifact 上传（删除跟踪不等于删除产物）
- 人读版本的基线报告落在 `docs/reports/load/`

## 完成记录

于 `docs/restructure` 分支随文档重构一并完成：

- `git rm --cached metrics-report.json k6/out/summary.json` —— 解除跟踪，文件保留在本地
- `.gitignore` 补上 `metrics-report.json`（`k6/out/` 之前已在，但对已跟踪文件无效，所以必须先 `rm --cached`）
- CI 的 `load-test` job 原本就把两者作为 artifact 上传（`.github/workflows/ci.yml:204,220`），无需改动
- 人读版本的基线报告落在 [`docs/reports/load/2026-05-29-baseline.md`](../../reports/load/2026-05-29-baseline.md)
