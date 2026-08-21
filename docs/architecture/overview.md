# 架构现状

> 本文描述 **截至 2026-08-19 代码里实际存在的东西**。计划中但未实现的组件不出现在这里。

## 1. 系统是什么

一个航班订票系统，拆成两个 Go 微服务，通过 gRPC 通信，各自持有独立数据库。外部只暴露一个 HTTP REST 入口。

选这个业务场景不是因为业务本身有趣，而是因为它天然带来运维体系需要处理的所有难点：

- **跨服务的状态一致性** —— 订单在 A 库，座位库存在 B 库，没有分布式事务
- **有限资源的并发竞争** —— 最后一个座位被两个人同时抢
- **失败传播** —— 下游挂了，上游怎么办
- **缓存一致性** —— 座位数变了，缓存里的旧值怎么办

## 2. 运行时拓扑

```
                        外部客户端 (HTTP)
                               │
                               ▼ :8080
                    ┌──────────────────────┐
                    │   booking-service    │  Go + Echo
                    │   REST API           │  订单所有权方
                    │                      │
                    │  ┌────────────────┐  │
                    │  │ 熔断器          │  │  CLOSED/OPEN/HALF_OPEN
                    │  │ 重试(指数退避)   │  │  1 次初始 + 最多 3 次重试
                    │  │ API Key 注入    │  │  x-api-key metadata
                    │  └────────────────┘  │
                    └───────┬──────────┬───┘
                            │          │ gRPC :50051
                     :5432  │          ▼
                   ┌────────▼──┐   ┌──────────────────────┐
                   │booking-db │   │   flight-service     │  Go + gRPC
                   │PostgreSQL │   │   航班/座位所有权方    │
                   │  :5434 →  │   │                      │
                   └───────────┘   │  ┌────────────────┐  │
                                   │  │ 认证拦截器      │  │
                                   │  │ 指标拦截器      │  │
                                   │  │ Cache-Aside    │  │
                                   │  └────────────────┘  │
                                   └───┬──────────────┬───┘
                                :5432  │              │
                              ┌────────▼──┐    ┌──────▼────────────┐
                              │ flight-db │    │  Redis 集群        │
                              │PostgreSQL │    │  master + replica  │
                              │  :5433 →  │    │  + 1× sentinel     │
                              └───────────┘    └───────────────────┘

  观测面：
    booking-service :8080/metrics ─┐
    flight-service  :9091/metrics ─┤
    postgres-exporter × 2          ├──► Prometheus :9090 ──► Grafana :3000
    redis-exporter                 ┘         │
                                             └──► Alertmanager :9093
```

共 **13 个容器服务**（`docker-compose.yml`）。

> 📌 文档漂移记录：根 `README.md` 里写的是"12 个容器"，实际是 13。这是一个很小但很典型的例子 —— 架构变了文档没跟上。修正它属于阶段 0 的杂务。

## 3. 服务职责边界

### booking-service（公开入口）

- 拥有 `bookings` 表 —— 订单是它的数据
- **不直接操作座位库存**，所有座位操作走 gRPC 委托给 flight-service
- 对外暴露 REST，路由由 OpenAPI 规范生成（`booking-service/api/openapi.yaml` → `api.gen.go`）
- 承载所有跨服务韧性逻辑：重试、熔断、认证注入

### flight-service（内部服务）

- 拥有 `flights` 和 `seat_reservations` 表
- **只暴露 gRPC**，外部不可直达；唯一的 HTTP 端口 `:9091` 只提供 `/metrics`
- 座位的增减是它的独占职责，用 `SELECT ... FOR UPDATE` 保证并发正确性
- 读路径带 Redis 缓存

这个边界划分的意义：**库存这种强一致资源，必须有唯一的所有权方**。如果 booking-service 也能直接改座位数，两个服务的并发控制就无法统一，`FOR UPDATE` 的锁也保护不了跨服务的竞争。

## 4. 关键数据流

### 创建订单（写路径，跨服务）

```
POST /bookings
  │
  ├─1─► GetFlight(flight_id)          gRPC，读航班信息与价格
  │       └─ flight-service: Redis 查缓存 → 未命中查 PG → 回填缓存
  │
  ├─2─► ReserveSeats(flight_id, n, booking_id)   gRPC，扣减库存
  │       └─ flight-service 单事务内：
  │            SELECT ... FOR UPDATE 锁住航班行
  │            检查 available_seats >= n
  │            UPDATE flights SET available_seats -= n
  │            INSERT seat_reservations (booking_id 唯一约束)
  │            COMMIT
  │            失效 flight:{id} 与相关 search:* 缓存
  │
  └─3─► INSERT INTO bookings (status=CONFIRMED, total_price=n×price)
```

**顺序是有意的**：先扣库存再落订单。如果反过来，扣库存失败时会留下一条无效订单。当前顺序下失败即中止，不会产生"订单存在但没座位"的状态。

**代价**：如果第 2 步成功但第 3 步失败（booking-db 挂了），座位被扣了但订单不存在 —— 库存泄漏。这是当前架构**已知的、未解决的**一致性缺口，见 [D-06](../tasks/D-06-seat-inventory-leak.md)。

### 幂等性

`seat_reservations.booking_id` 上有唯一约束。同一个 `booking_id` 重复调用 `ReserveSeats` 不会重复扣减库存。**这是重试机制能安全存在的前提** —— 没有幂等就不能重试，重试会导致重复扣库存。

### 取消订单

```
POST /bookings/{id}/cancel
  ├─► ReleaseReservation(booking_id)   gRPC，归还座位（同样单事务 + 缓存失效）
  └─► UPDATE bookings SET status=CANCELLED
```

## 5. 韧性机制（都在 booking-service 侧）

位置：`booking-service/internal/grpcclient/flight.go`、`booking-service/internal/circuitbreaker/breaker.go`

| 机制 | 参数 | 实现要点 |
|---|---|---|
| 重试 | 1 次初始调用 + 最多 3 次重试，退避 100/200/400ms | **只重试 `UNAVAILABLE` 和 `DEADLINE_EXCEEDED`**。`NOT_FOUND`、`INVALID_ARGUMENT`、`RESOURCE_EXHAUSTED` 立即返回 —— 这类错误重试一万次结果也一样，重试只会放大下游压力 |
| 熔断器 | 窗口 60s 内 5 次失败 → OPEN；OPEN 保持 30s → HALF_OPEN | 三态机；OPEN 状态下立即返回 `503` 而不是等超时。参数由 `CB_*` 环境变量注入 |
| 服务间认证 | `x-api-key` gRPC metadata | flight-service 侧 unary 拦截器对**所有**方法校验；不匹配返回 `UNAUTHENTICATED` |

熔断器被设计成**独立包 + 泛型包装函数**（`withCircuitBreaker[T]`），不侵入业务逻辑。这是对的：韧性策略应该能独立于业务演进和测试。

> ⚠️ 熔断器的失败判定当前存在缺陷（业务错误被计入熔断统计），见 [D-01](../tasks/D-01-circuit-breaker-error-classification.md)。

## 6. 缓存策略

`flight-service/internal/cache/redis.go`，Cache-Aside 模式。

| 键 | TTL | 失效时机 |
|---|---|---|
| `flight:{id}` | 5 分钟 | `ReserveSeats` / `ReleaseReservation` 后显式删除 |
| `search:{origin}:{destination}:{date}` | 5 分钟 | 同上，删除精确键 + 无日期变体 |

Redis 通过 **Sentinel 模式**接入（`redis.NewFailoverClient`），客户端向 sentinel 询问 master 地址，master 挂掉后能自动跟随 failover 到新 master，不需要重启服务。

> ⚠️ 当前只部署了 **1 个 sentinel、quorum=1**，不构成真正的高可用，见缺陷 D-04。

## 7. 数据模型

```
flights                      seat_reservations              bookings
  id            PK             id             PK              id              PK
  flight_number                flight_id      FK ──► flights  user_id
  airline                      booking_id     UNIQUE ┐        flight_id  (跨服务软引用)
  origin (IATA)                seat_count            │        passenger_name
  destination (IATA)           status                │        passenger_email
  departure_time               created_at            │        seat_count
  arrival_time                                       │        total_price  (下单时价格快照)
  total_seats     CHECK > 0                          │        status
  available_seats CHECK >= 0                         └────────  id  (逻辑对应)
  price           CHECK > 0
  status
      ▲ flight_db                    ▲ flight_db              ▲ booking_db
```

**跨库引用是逻辑的，不是外键的** —— `bookings.flight_id` 和 `seat_reservations.booking_id` 分别指向另一个库，数据库层面无法约束。这是微服务拆库的固有代价，一致性只能靠应用层保证。

约束都下沉到数据库：`available_seats >= 0`、`available_seats <= total_seats`、`price > 0`、航班号+起飞日期唯一索引。**数据库约束是最后一道防线** —— 应用层逻辑有 bug 时，它能保证不产生脏数据（比如负数库存）。

迁移在服务启动时自动执行（golang-migrate，`file://migrations`）。

## 8. 观测面

### 指标

两个服务导出同一组指标名，用 `service` 标签区分：

| 指标 | 类型 | 标签 |
|---|---|---|
| `http_requests_total` | counter | service, method, endpoint, status |
| `http_request_errors_total` | counter | service, method, endpoint, error_type |
| `http_request_duration_seconds` | histogram | service, method, endpoint |

flight-service 是 gRPC，但**刻意复用 `http_*` 指标名**（`flight-service/internal/metrics/metrics.go:19`），这样一条 PromQL 能同时覆盖两个服务。

**基数控制**：HTTP 侧的 `endpoint` 用路由模板 `/bookings/:id` 而非实际 URL；gRPC 侧用 `info.FullMethod`。如果用实际 URL，每个 UUID 都会变成一个新时间序列，Prometheus 内存会被打爆 —— 这是监控系统最常见的事故原因之一。

### 采集与告警

- Prometheus 抓 **6 个 target**，间隔 5s：两个服务、两个 postgres_exporter、redis_exporter、自身
- 告警规则 `prometheus/alerts.yml`：`HighErrorRate`（错误率 >5% 持续 2m）、`HighLatencyP95`（p95 >1s 持续 2m）、`ServiceDown`（target down >1m）
- Alertmanager 只做聚合展示，**没有接任何外部通知渠道**（receiver 为空）
- Grafana 两块看板通过 provisioning 从代码加载：Services（RPS / 延迟分位 / 错误率）、Infrastructure（PG 连接数、事务速率、缓冲命中率、Redis ops/内存/客户端）

> ⚠️ `HighErrorRate` 当前把客户端错误（4xx / gRPC NOT_FOUND）计入错误率，见缺陷 D-02。

### SLI / SLO

| SLI | SLO | 失败阈值 |
|---|---|---|
| API 可用性 = 1 − 错误率(5m) | > 99% | < 95% |
| 延迟 p95(5m) | < 500ms | > 1000ms |
| 服务存活 `up{...}` | = 1 | 持续 0 超过 1m |

阈值来自实测基线：`metrics-report.json` 记录的压测结果为 **错误率 0%、p95 4.86ms**。SLO 相对基线留了两个数量级的余量。

`scripts/verify_metrics.py` 在 CI 里查询 Prometheus 验证这些阈值，违反则 exit 1 —— **SLO 不只是文档，是能卡住合并的门禁**。

## 9. 测试与 CI

| 层级 | 工具 | 覆盖 |
|---|---|---|
| 单元 | `go test -race` | 熔断器状态机、gRPC handler |
| 集成 + E2E | pytest（16 个用例） | 全栈起容器，走真实 HTTP；E2E 用例直连两个 PostgreSQL 校验 `bookings` 行、`seat_reservations` 行、`available_seats` 数值在 create → cancel 全周期的正确性 |
| 负载 | k6 | 三个场景，见下表；CI 只跑 `steady` 且降速降标准 |

`k6/script.js` 由 `SCENARIO` / `MODE` 两个环境变量选择跑什么：

| `SCENARIO` | 打谁 | 发压模式 | 回答什么 |
|---|---|---|---|
| `steady` | 多航班，读写 20:1 混合 | 恒定 500 QPS | 平峰下 p95 与错误率达标吗 |
| `read` | 打散到 50 个航班，只压 `GET /flights/{id}` | `MODE=recon` 闭环 / `MODE=ladder` 开环阶梯 | 读路径吞吐上限与拐点 |
| `write` | 锁定 1 个航班，只压 `POST /bookings` | 同上 | 单航班行锁上限与过载后的表现 |

两个阶梯场景须先 `recon` 后 `ladder`：`recon` 用 `ramping-vus` 测出吞吐平台 `X_max`，`ladder` 再用 `ramping-arrival-rate` 自 `0.5×` 铺到 `1.5×X_max`。入口是 `Makefile` 的 `loadtest-*` 目标，分档曲线由 `k6/script.js:386` 的 `renderLadder` 在跑完时直接打印，并存一份 `k6/out/<run>.report.json`。

409 被 `setResponseCallback` 排除出 `http_req_failed`（`k6/script.js:53`）—— 座位不足是正常业务拒绝，不是故障。

压测航班由 `k6/loadtest-seed.sql` 单独灌入（`make loadtest-seed`），**不在迁移里**：它是测试装置，不是系统的一部分。写场景那个航班 500 万座，保证整个压测窗口内库存不会耗尽。

CI（`.github/workflows/ci.yml`）四个 job：

```
build ─┐
       ├─► integration  （compose 起栈 → 等就绪 → 查 Prometheus 确认 target up → pytest → 导出容器日志）
unit ──┘
       └─► load-test    （compose 起栈 → 灌压测航班 → k6 steady 烟雾跑 → verify_metrics.py 查 Prometheus 验 SLI）
```

`load-test` 里的 k6 **不是容量门禁**：runner 规格与开发机差一个数量级，判不了容量（[`conventions/testing.md`](../conventions/testing.md) § 6）。它只回答"跑得起来、没崩、没有数量级退化"。容量结论只能来自开发机跑 `read` / `write` 阶梯，产出写入 `docs/reports/load/`。

## 10. 当前架构的定位

用一句话概括：**这是一个"开发得很好、但还没有被运维过"的系统。**

它具备了良好的可运维基础 —— 配置即代码、指标齐全、有 SLO 门禁。但它缺少运维体系真正需要的东西：编排层、发布流程、日志聚合、链路追踪、故障预案、优雅停机。

下一步做什么，见 [tasks/](../tasks/README.md) 和 [plans/roadmap.md](../plans/roadmap.md)。
