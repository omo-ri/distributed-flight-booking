# 工程规范

> 这份规范服务于一个具体目标：让这个仓库看起来、也确实运作得像一个**被专业运维的生产系统**，而不是一堆作业代码。
> 规范本身也是学习内容 —— 面试官看仓库时，提交历史和目录结构传递的信息不比代码少。

## 1. 分支与提交

### 分支

```
main                    始终可部署，受保护，只接受 PR 合并
feat/<简短描述>          新增能力       feat/k8s-manifests
fix/<简短描述>           修复缺陷       fix/cb-business-errors
docs/<简短描述>          纯文档         docs/runbooks
chore/<简短描述>         杂务           chore/dockerignore
```

**不在 main 上直接提交。** 即使是单人项目 —— 因为运维工作的核心纪律就是"变更必须经过流程"，在自己的项目上养成习惯，比在生产上第一次学要便宜得多。

### 提交信息

Conventional Commits：

```
<type>(<scope>): <一句话，祈使句，不加句号>

<可选正文：为什么这么改，不是改了什么>

<可选脚注：Refs: D-01>
```

**type 是封闭集合**：`feat` `fix` `docs` `refactor` `test` `perf` `build` `ci` `chore`

**scope 必填，但取值开放。** 写"这次改动影响的范围"，让人在 `git log --oneline` 里一眼看出该不该细看这条。

不维护固定枚举 —— 项目会长出新的部分（`k8s`、`loki`、`rollout`、`chaos`……），每次都要先改规范才能提交，这个摩擦只会导致规范被绕过。常用值作参考：

```
booking  flight  proto  cache  cb        服务与组件
obs  alerts  slo  grafana                可观测性
ci  build  deps  compose  k8s            工程设施
docs  structure  tasks  runbook          文档
```

scope 的约束只有四条：

- **小写，一个词**（必要时用连字符：`error-budget`）
- **描述范围，不描述动作** —— 动作已经在 type 和标题里了
- **复用已有的** —— 同一个东西每次换个叫法，等于没有 scope
- **想不出 scope 说明这次提交太杂了** —— 该拆成几个提交，而不是硬凑一个

```
✅ fix(booking): 熔断器不再把业务错误计入失败统计
   NOT_FOUND 和 RESOURCE_EXHAUSTED 表示下游工作正常，
   只是答案为空。把它们计入失败会让 5 次 404 就打开熔断，
   一个用户的错误输入导致全体不可用。
   Refs: D-01

✅ docs(structure): 按文档生命周期重构 docs/ 目录
✅ feat(obs): 两个服务统一 slog JSON 输出
✅ chore(deps): 升级 pgx 到 v5.7

❌ fix bug                        除了 type 什么信息都没有
❌ update                         同上
❌ docs: 重构目录                  缺 scope
❌ fix(修复熔断器): ...             scope 写成了动作
❌ feat(booking-service-internal-grpcclient): ...   scope 不是文件路径
```

**正文写"为什么"**。三个月后回头看，"改了什么"从 diff 就能看出来，"为什么"只有当时的你知道。修复本仓库已记录的缺陷时，脚注带上 `Refs: D-xx`。

### PR

即使自己合并自己的 PR，也要写清楚：改了什么、为什么、怎么验证的、有什么风险。PR 描述是未来复盘的一手材料。

CI 全绿才能合并。**永远不要为了合并而临时放宽 SLO 阈值** —— 门禁一旦可以被绕过就不再是门禁。

## 2. 目录约定

```
distributed-flight-booking/
├── docs/                       工程文档（中文），分类见 docs/conventions/documentation.md
│   ├── architecture/           【事实】系统现在是什么
│   ├── conventions/            【规范】动手前读
│   ├── plans/                  【意图】路线图、能力差距
│   ├── tasks/                  【意图】可执行条目，一条一文件
│   ├── knowledge/              【知识】按主题，与本仓库解耦
│   ├── runbooks/               【操作】告警处置手册，一条告警一个文件
│   └── reports/                【记录】带日期，写完不改
│       ├── postmortems/        故障复盘，YYYY-MM-DD-<事件>.md
│       ├── load/               压测报告
│       └── reviews/            周期性 SLO 评审
├── proto/                      gRPC 契约（源）
├── <service>/
│   ├── cmd/                    入口，只做依赖组装
│   ├── api/                    OpenAPI 契约 + 生成代码
│   ├── internal/               业务实现
│   └── migrations/             SQL 迁移
├── deploy/                     部署清单（阶段 2 新建）
│   ├── base/                   Kustomize base
│   └── overlays/               dev / staging / prod
├── observability/              可观测性栈配置（阶段 1 收拢）
│   └── prometheus/  grafana/  alertmanager/  loki/
├── chaos/                      故障注入场景（阶段 5 新建）
├── scripts/                    运维脚本
├── tests/                      集成 + E2E
├── k6/                         负载脚本
└── .github/workflows/
```

**规则**：
- 生成的代码提交进仓库（`*.pb.go`、`api.gen.go`），但**必须能通过 `make` 重新生成**。契约是源，生成物是产物 —— 手改生成物是不允许的。
- 运行时产物（`metrics-report.json`、`k6/out/`）不进仓库，只作为 CI artifact。
- 每个新目录带一个 `README.md` 说明它是什么、怎么用。

## 3. 配置

### 分层

| 层 | 用途 | 位置 |
|---|---|---|
| 代码默认值 | 本地能直接 `go run` 跑起来 | `envOrDefault(key, fallback)` |
| 环境变量 | 部署环境差异 | Compose / K8s ConfigMap |
| 密钥 | 凭据 | `.env`（gitignored）→ K8s Secret → SOPS |

**硬性规则**：
- 密钥不进 Git，一次都不行。**进过 Git 历史就等于永久泄漏**，改文件救不回来，只能轮换凭据。
- 环境变量名全大写下划线，同一个概念在所有服务里用同一个名字。
- 新增配置项必须同步更新 `README.md` 的环境变量表。
- 配置读取集中在启动阶段，不在业务代码里随手 `os.Getenv`。

### 配置即代码

所有基础设施配置（Prometheus 规则、Grafana 看板、告警路由、K8s 清单）必须在仓库里，通过 provisioning / GitOps 加载。

**禁止在 UI 上点配置。** 在 Grafana 界面上改的看板，重启就没了，也没人知道是谁在什么时候为什么改的。

## 4. 可观测性规范

### 日志

```go
// 统一 slog JSON，两个服务都是
log.Info("seats reserved",
    "trace_id", traceID,
    "booking_id", bookingID,
    "flight_id", flightID,
    "seat_count", n,
)
```

**规则**：
- 两个服务统一用 `slog` + JSON handler（当前 flight-service 用标准库 `log`，属于待修项 D-11）
- 每条日志必须带 `trace_id`，跨服务通过 gRPC metadata 传递
- **日志级别的判据是"谁需要看"**：
  - `Error` —— 需要人介入的问题（会触发告警的那种）
  - `Warn` —— 异常但已自动处理（重试成功、降级生效、熔断打开）
  - `Info` —— 状态变更与关键业务事件（启动、配置加载、订单创建）
  - `Debug` —— 排障细节，生产默认关闭
- **不要在每个正常请求上打日志**（当前 `[AUTH] OK` 和 cache HIT/MISS 就是反例）。高频路径的日志成本是真实的，而且会淹没有用的信息。请求级信息用指标表达，只在异常时打日志。
- 日志里不出现密钥、token、密码、完整身份证/手机号

### 指标

- 命名遵循 Prometheus 惯例：`<域>_<对象>_<单位>_<后缀>`，counter 以 `_total` 结尾，时间用秒
- **新增标签前先问：这个标签的取值集合有上界吗？** 无界标签（UUID、URL、用户 ID、错误消息）绝对禁止
- 直方图的桶要覆盖实际分布，不能照抄默认值
- 每个韧性组件都要有指标：熔断器状态、重试次数、缓存命中率、连接池使用率。**没有指标的机制等于不存在** —— 你不知道它有没有生效

### 告警

每条告警规则必须有：

```yaml
- alert: <名字>
  expr: <表达式>
  for: <持续时间>
  labels:
    severity: critical | warning | info
    sli: <对应哪个 SLI>
  annotations:
    summary: "一句话说明发生了什么"
    description: "带上下文和当前数值"
    runbook_url: "https://github.com/<repo>/blob/main/docs/runbooks/<name>.md"   # 必填
```

**判据：这条告警响了，有人需要立刻做点什么吗？** 答案是"不"的，就不该是告警，应该是看板上的一条线。

告警疲劳是运维团队最常见的失败模式 —— 一旦有人开始习惯性忽略告警，整套告警体系就失效了。

### Runbook

`docs/runbooks/<告警名>.md`，固定结构：

```markdown
# <告警名>

## 这条告警意味着什么
## 用户影响面
## 立即诊断（可复制粘贴的命令 / 看板链接）
## 常见原因（按概率排序）
## 止血手段
## 升级路径
## 相关：历史复盘链接
```

写 runbook 的时机是**建告警的时候**，不是故障发生的时候。

## 5. 变更纪律

任何影响运行时行为的变更，必须能回答：

1. **怎么验证它生效了？** —— 具体的命令、看板、日志或指标
2. **怎么知道它出问题了？** —— 哪条告警会响
3. **怎么回滚？** —— 具体步骤

答不出来的变更不要合并。这三个问题是运维思维和开发思维的分水岭。

## 6. 测试要求

| 变更类型 | 最低要求 |
|---|---|
| 业务逻辑 | 单元测试 + 集成测试 |
| 韧性机制（重试/熔断/超时） | 单元测试覆盖状态迁移与边界 |
| 数据库迁移 | up 和 down 都要能跑；在有数据的库上验证过 |
| 指标 / 告警 | 能实际触发一次（`demo_alerts.sh` 或 chaos 场景） |
| K8s 清单 | 在 kind 集群上完整 apply 过 |
| Runbook | 照着做一遍，确认命令都能跑 |

`go test` 一律带 `-race`。

## 7. 文档纪律

完整的文档分类、命名、链接与复盘文档要求见 [documentation.md](./documentation.md)。这里只列三条不可协商的：

- 代码改了，对应文档同一个 PR 里改。**不允许"回头再补"**
- `docs/architecture/` 只描述已存在的东西
- 能力实现了，把 `docs/tasks/D-xx.md` 移进 `docs/tasks/done/` 并把 `status` 改成 `done`，同时加进 `docs/architecture/capabilities.md`，带上代码位置
- 每个故障演练产出一份 `docs/reports/postmortems/` 文档，**无论演练成功与否**

## 8. 依赖与版本

- 镜像 tag 固定到具体版本（当前 compose 做得对：`prom/prometheus:v2.55.0` 而不是 `latest`）
- Go 依赖用 `go mod tidy` 维护，不手改 `go.mod`
- 升级依赖单独提交，不和功能变更混在一起

## 9. 命名

| 对象 | 约定 | 例 |
|---|---|---|
| Go 包 | 小写单词，不用下划线 | `circuitbreaker` |
| 环境变量 | 大写下划线 | `CB_ERROR_THRESHOLD` |
| 指标 | 小写下划线 + 单位后缀 | `http_request_duration_seconds` |
| K8s 资源 | kebab-case | `booking-service` |
| 文档文件 | kebab-case，目录承担分类，不加全局序号 | `gap-analysis.md` |
| 任务文档 | `<ID>-<kebab 描述>.md` | `D-01-circuit-breaker-error-classification.md` |
| 复盘文档 | `YYYY-MM-DD-<事件>.md` | `2026-09-03-redis-failover.md` |

## 10. 一个待处理的仓库设置

`.gitignore` 当前忽略了 `CLAUDE.md` 和 `.claude/`。等做到阶段 6（自动化 / vibe coding）时需要重新考虑 —— 项目级的 AI 协作规范（构建命令、代码约定、常见陷阱）应该**进版本控制**，它和 README 一样是项目资产。个人偏好配置（`.claude/settings.local.json`）继续忽略。
