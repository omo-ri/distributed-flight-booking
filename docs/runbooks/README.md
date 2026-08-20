# Runbooks

一条告警一个文件，文件名与告警名完全一致；写在**建告警的时候**——规则见 [`CLAUDE.md`](../../CLAUDE.md) § 3，理由见 [conventions/engineering.md § 5](../conventions/engineering.md)。

`prometheus/alerts.yml` 里每条规则的 `annotations.runbook_url` 必须指向这里的对应文件。

`prometheus/alerts.yml` 定义的三条告警（`HighErrorRate:5`、`HighLatencyP95:19`、`ServiceDown:34`）在本目录都没有对应文件，三条规则也都不带 `runbook_url` 注解 —— 见 [D-14](../tasks/D-14-alert-runbooks.md)。

## 固定结构

```markdown
# <告警名>

## 这条告警意味着什么
一句话。不要复述 PromQL 表达式，说人话。

## 用户影响面
谁受影响、影响到什么程度。"内部指标异常，用户无感"也是一种合法答案。

## 立即诊断
可复制粘贴的命令、看板链接。**要能直接跑，不要写"检查一下日志"。**

## 常见原因
按概率排序，每条带对应的判别方法。

## 止血手段
恢复服务优先于定位根因。每条要说明副作用。

## 升级路径
什么情况下该叫人、叫谁。

## 相关
历史复盘链接（reports/postmortems/）、相关任务、相关 runbook。
```

## 维护

- runbook 里的命令会过期（资源名、端口、看板 URL 变了）。[`CLAUDE.md`](../../CLAUDE.md) § 4 要求：**改 runbook 时照着做一遍，确认命令都能跑**
- 每次真实故障或演练之后，回来更新对应 runbook —— 演练里发现的"这步实际不管用"是最有价值的修订来源
