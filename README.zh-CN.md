# 航班订票系统 —— 使用说明

> 俄语版完整说明（含架构、机制原理、SLI/SLO、CI）见 [`README.md`](./README.md)；
> 工程文档（架构事实、规范、任务、runbook）见 [`docs/README.md`](./docs/README.md)。
> 这份文档只讲**怎么把它跑起来、怎么用**。

两个 Go 微服务：`booking-service`（对外 REST，:8080）通过 gRPC 调用 `flight-service`（内部服务，:50051，不暴露业务 HTTP 接口）。各自独占一个 PostgreSQL，flight-service 用 Redis Sentinel 做读缓存。整套栈 12 个容器，`docker compose` 一条命令起。

---

## 1. 启动

前置：Docker（带 compose 插件）、Python 3（只用来跑测试）。

```bash
make run     # 构建 + 启动 + 等就绪 + 跑 pytest
```

`make run` 会先 `pip install -r tests/requirements.txt`，再 `docker compose up --build -d`，然后轮询 `:8080/metrics` 和 `:9091/metrics` 直到两个服务都能应答，最后执行 `pytest tests/ -v`（`Makefile:20-30`）。

不想跑测试就分开来：

```bash
make up       # 等价于 docker compose up --build -d
make test     # 服务已经在跑时，只跑 pytest
make stop     # docker compose down -v，同时删掉数据卷
```

数据库迁移和测试数据在服务启动时自动执行，不需要手动灌数据。

## 2. 端口

| 端口 | 用途 |
|---|---|
| 8080 | booking-service —— REST API 和 `/metrics` |
| 50051 | flight-service —— gRPC |
| 9091 | flight-service —— `/metrics` |
| 9090 | Prometheus |
| 9093 | Alertmanager |
| 3000 | Grafana（匿名可看；要编辑用 `admin/admin`） |
| 5433 / 5434 | flight-db / booking-db |

## 3. 预置数据

`flight-service/migrations/002_seed.up.sql` 灌三个航班：

| ID | 航班号 | 航线 | 起飞 | 座位 | 价格 |
|---|---|---|---|---|---|
| `a0eebc99-…-6bb9bd380a11` | SU1234 | SVO → LED | 2026-04-01 10:00 | 180 | 15000 |
| `b0eebc99-…-6bb9bd380a22` | SU5678 | SVO → LED | 2026-04-01 18:00 | 120 | 12000 |
| `c0eebc99-…-6bb9bd380a33` | DP402 | VKO → LED | 2026-04-02 08:00 | 189 | 8000 |

## 4. 调用 API

对外只有 booking-service 一个入口，基址 `http://localhost:8080`。契约源文件是 [`booking-service/api/openapi.yaml`](./booking-service/api/openapi.yaml)，可以直接导进 Postman / Swagger UI。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/flights?origin=&destination=&date=` | 按航线搜航班 |
| GET | `/flights/{id}` | 按 ID 取航班 |
| POST | `/bookings` | 创建订单 |
| GET | `/bookings/{id}` | 按 ID 取订单 |
| GET | `/bookings?user_id=` | 用户的订单列表 |
| POST | `/bookings/{id}/cancel` | 取消订单并归还座位 |
| GET | `/metrics` | Prometheus 指标 |

本章所有请求和响应都是在本机 `docker compose up` 起的栈上实跑出来的，不是照抄契约。

### 4.1 通用约定

- **不需要鉴权**。API Key 只用在 booking-service → flight-service 的内部 gRPC 调用上，对外接口不校验任何凭据。
- **没有用户系统**。`user_id` 由调用方自己给，任意 UUID 都行，服务端不检查它是否"存在"——`bookings.user_id` 只是一列 UUID 加一个索引，没有外键（`booking-service/migrations/001_init.up.sql:3`）。同一个 `user_id` 下的订单会被 `GET /bookings?user_id=` 归到一起，仅此而已。
- **POST 必须带 `Content-Type: application/json`**。漏了 Echo 不会解析 body，所有字段变成零值，你收到的是 `400 seat_count must be at least 1`——报错和真实原因对不上，别被带偏。
- **所有 id 都是 UUID**。路径参数或查询参数格式不对，在进业务逻辑之前就 400（`booking-service/api/api.gen.go:183`、`:217`）。
- **金额是整数，单位是最小货币单位**。`price: 15000` 表示 150.00，响应里不会出现小数。
- **时间是 RFC3339 UTC**：`2026-04-01T10:00:00Z`。
- **错误体统一是 `{"message": "..."}`**，所有状态码都一样。

### 4.2 订一张票：完整四步

#### 第 1 步：搜航班，拿 `flight_id`

下单需要 `flight_id`，它只能从搜索结果里来。

```bash
curl 'http://localhost:8080/flights?origin=SVO&destination=LED&date=2026-04-01'
```

```json
[
  {
    "id": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
    "flight_number": "SU1234",
    "airline": "Aeroflot",
    "origin": "SVO",
    "destination": "LED",
    "departure_time": "2026-04-01T10:00:00Z",
    "arrival_time": "2026-04-01T11:30:00Z",
    "total_seats": 180,
    "available_seats": 180,
    "price": 15000,
    "status": "SCHEDULED"
  },
  { "id": "b0eebc99-9c0b-4ef8-bb6d-6bb9bd380a22", "flight_number": "SU5678", "…": "…" }
]
```

`available_seats` 是当前余位，`price` 是**单座**价格。记下要订的那班的 `id`。

#### 第 2 步：下单

```bash
curl -X POST http://localhost:8080/bookings \
  -H 'Content-Type: application/json' \
  -d '{
    "user_id": "550e8400-e29b-41d4-a716-446655440000",
    "flight_id": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
    "passenger_name": "Ivan Ivanov",
    "passenger_email": "ivan@example.com",
    "seat_count": 2
  }'
```

`201 Created`：

```json
{
  "id": "8e56216a-14b0-486e-8b9a-2a42099cb893",
  "user_id": "550e8400-e29b-41d4-a716-446655440000",
  "flight_id": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
  "passenger_name": "Ivan Ivanov",
  "passenger_email": "ivan@example.com",
  "seat_count": 2,
  "total_price": 30000,
  "status": "CONFIRMED",
  "created_at": "2026-08-20T11:18:57.888405Z"
}
```

返回的 `id` 就是**订单号**，后面查询和取消都用它——请把它存下来，没有别的办法找回单张订单（只能靠 `user_id` 列表捞）。

`total_price` = `seat_count` × 下单**那一刻**的单座价格，之后航班改价不影响已有订单（`booking-service/internal/service/booking.go:150`）。

下单是一次跨服务操作：`GetFlight`（取价）→ 生成订单 ID → `ReserveSeats`（扣位）→ 落库。座位没扣成功就**不会**产生订单（`booking-service/internal/service/booking.go:100-171`）。

再查一次这班航班，会看到余位从 180 变成 178：

```bash
curl http://localhost:8080/flights/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11
```

#### 第 3 步：查订单

```bash
curl http://localhost:8080/bookings/8e56216a-14b0-486e-8b9a-2a42099cb893
curl 'http://localhost:8080/bookings?user_id=550e8400-e29b-41d4-a716-446655440000'
```

前者返回单个订单对象，后者返回数组（按 `created_at` 倒序，最新的在最前）。

#### 第 4 步：取消

```bash
curl -X POST http://localhost:8080/bookings/8e56216a-14b0-486e-8b9a-2a42099cb893/cancel
```

`200 OK`，返回的是更新后的订单，`status` 变成 `CANCELLED`，座位同时归还给航班（余位回到 180）。再取消一次得到 `409 booking already cancelled`。

取消是**终态**：没有"恢复订单"这个接口，想再订就重新下单。

### 4.3 接口参考

#### `GET /flights` —— 搜航班

| 参数 | 位置 | 必填 | 说明 |
|---|---|---|---|
| `origin` | query | 是 | 出发地 IATA 三字码，如 `SVO` |
| `destination` | query | 是 | 目的地 IATA 三字码，如 `LED` |
| `date` | query | 否 | 出发日期，严格 `YYYY-MM-DD`。按出发时间的**日期部分**过滤 |

只返回 `status = SCHEDULED` 的航班，按 `departure_time` 升序（`flight-service/internal/repository/flight.go:44-58`）。航线大小写敏感，`svo` 搜不到东西。

| 情况 | 状态码 | 响应体 |
|---|---|---|
| 有结果 | 200 | `Flight` 数组 |
| 没有匹配航班 | 200 | `[]`（空数组，不是 404） |
| 缺 `origin` / `destination` | 400 | `{"message":"Invalid format for parameter origin: query parameter 'origin' is required"}` |
| `date` 格式不对 | 400 | `{"message":"Invalid format for parameter date: parsing time \"2026/04/01\" …"}` |
| `origin=`（传了但是空串） | 500 | 见 [4.4 已知坑](#44-已知坑) |
| 熔断器 OPEN | 503 | `{"message":"service temporarily unavailable"}` |

#### `GET /flights/{id}` —— 按 ID 取航班

下单前确认余位和价格用这个。`id` 必须是合法 UUID，否则 400（不查库）。

| 情况 | 状态码 | 响应体 |
|---|---|---|
| 找到 | 200 | `Flight` 对象 |
| `id` 不是 UUID | 400 | `{"message":"Invalid format for parameter id: …invalid UUID length: 3"}` |
| 航班不存在 | 404 | `{"message":"flight not found"}` |
| 熔断器 OPEN | 503 | `{"message":"service temporarily unavailable"}` |

`Flight` 字段：`id`、`flight_number`、`airline`、`origin`、`destination`、`departure_time`、`arrival_time`、`total_seats`、`available_seats`、`price`（单座，最小货币单位）、`status`（`SCHEDULED` / `DEPARTED` / `CANCELLED` / `COMPLETED`）。

#### `POST /bookings` —— 创建订单

请求体五个字段，契约上全部必填 —— 但实现不校验，缺字段不会得到 400，见 [4.4](#44-已知坑)：

| 字段 | 类型 | 说明 |
|---|---|---|
| `user_id` | UUID | 调用方自己给，见 [4.1](#41-通用约定) |
| `flight_id` | UUID | 从搜索结果里拿 |
| `passenger_name` | string | 最长 200 字符 |
| `passenger_email` | string | 最长 200 字符，**不做格式校验** |
| `seat_count` | int | ≥ 1，且 ≤ 当前余位 |

| 情况 | 状态码 | 响应体 |
|---|---|---|
| 成功 | 201 | `Booking` 对象 |
| `seat_count` ≤ 0，或 JSON 语法错、UUID 格式错 | 400 | `{"message":"seat_count must be at least 1"}` / `{"message":"invalid request body"}` |
| 航班不存在 | 404 | `{"message":"flight not found"}` |
| 余位不足 | 409 | `{"message":"insufficient seats"}` |
| 熔断器 OPEN | 503 | `{"message":"service temporarily unavailable"}` |

**没有幂等键**。同样的请求体发两次会产生两个订单、扣两次座位——重复提交要由调用方自己防。（服务内部对 `ReserveSeats` 是幂等的，靠下单时预先生成的 `booking_id`，但那个 ID 不由客户端提供，见 [`docs/architecture/contracts.md`](./docs/architecture/contracts.md)。）

#### `GET /bookings/{id}` —— 按 ID 取订单

| 情况 | 状态码 | 响应体 |
|---|---|---|
| 找到 | 200 | `Booking` 对象 |
| `id` 不是 UUID | 400 | `{"message":"Invalid format for parameter id: …"}` |
| 订单不存在 | 404 | `{"message":"booking not found"}` |

只读本地库，不走 gRPC，所以 flight-service 挂了这个接口照样可用。

`Booking` 字段：`id`、`user_id`、`flight_id`、`passenger_name`、`passenger_email`、`seat_count`、`total_price`（总价快照）、`status`（`CONFIRMED` / `CANCELLED`）、`created_at`。

#### `GET /bookings?user_id=` —— 用户的订单列表

| 情况 | 状态码 | 响应体 |
|---|---|---|
| 有订单 | 200 | `Booking` 数组，`created_at` 倒序 |
| 该用户没有订单 | 200 | `[]` |
| 缺 `user_id` 或格式不对 | 400 | `{"message":"Invalid format for parameter user_id: query parameter 'user_id' is required"}` |

没有分页、没有状态过滤，一次返回该用户的全部订单（含已取消的）。

#### `POST /bookings/{id}/cancel` —— 取消订单

请求没有 body。

| 情况 | 状态码 | 响应体 |
|---|---|---|
| 取消成功 | 200 | `Booking` 对象，`status = CANCELLED` |
| `id` 不是 UUID | 400 | `{"message":"Invalid format for parameter id: …"}` |
| 订单不存在 | 404 | `{"message":"booking not found"}` |
| 已经取消过 | 409 | `{"message":"booking already cancelled"}` |

归还座位是 **best-effort**：`ReleaseReservation` 失败只记日志，订单照样置为 `CANCELLED` 并返回 200（`booking-service/internal/service/booking.go:217-234`）。也就是说取消返回 200 不等于座位一定回到了库存。

#### `GET /metrics` —— Prometheus 指标

纯文本格式，给 Prometheus 抓的，不是给人读的。想看图去 Grafana（[第 6 节](#6-看监控)）。

### 4.4 已知坑

这几条是实跑出来的行为，和直觉不一致，先知道能省很多调试时间：

- **`?origin=&destination=LED` 返回 500 而不是 400**，响应体还会把下游 gRPC 错误原样透出。空串通过了生成代码的必填检查，到 flight-service 才被拒，而 booking-service 没把 `INVALID_ARGUMENT` 映射成 400 —— [D-18](./docs/tasks/D-18-invalid-argument-mapped-to-500.md)。
- **请求体的必填字段没人校验。** 漏 `flight_id` 得到 `404 flight not found`（缺字段变成全零 UUID，拿去查航班当然查不到）；漏 `user_id` 更糟 —— **返回 201，订单挂在 `00000000-0000-0000-0000-000000000000` 名下**。只有 `seat_count` 有显式校验 —— [D-19](./docs/tasks/D-19-request-body-not-validated.md)。
- **5xx 会把内部错误细节吐给你**，包括 SQLSTATE、列类型和长度上限（例如 `passenger_name` 超过 200 字符时）。调试时这很方便，但它不该出现在公开响应里 —— [D-20](./docs/tasks/D-20-internal-errors-leaked-to-clients.md)。顺带一提，这种 500 还会漏座位：座位已扣、订单没落库，见 [D-06](./docs/tasks/D-06-seat-inventory-leak.md)。
- **连查 5 个不存在的航班，之后连正常请求都返回 503。** 熔断器把 `NOT_FOUND`、`RESOURCE_EXHAUSTED` 这类业务错误也计进了失败统计，60 秒内攒够 5 次就打开，30 秒内拒绝所有请求 —— [D-01](./docs/tasks/D-01-circuit-breaker-error-classification.md)。

完整的错误码映射（业务情况 → gRPC code → HTTP）和背后的取舍见 [`docs/architecture/contracts.md`](./docs/architecture/contracts.md)。

## 5. 常用环境变量

都有代码默认值，`docker compose` 下不用配也能跑；直接 `go run` 时才可能需要覆盖。

| 变量 | 默认值 | 作用 | 服务 |
|---|---|---|---|
| `HTTP_PORT` | `8080` | REST 监听端口 | booking |
| `GRPC_PORT` | `50051` | gRPC 监听端口 | flight |
| `METRICS_PORT` | `9091` | `/metrics` 端口 | flight |
| `FLIGHT_SERVICE_ADDR` | `localhost:50051` | 下游 gRPC 地址 | booking |
| `AUTH_API_KEY` | 空 | 服务间 API Key，两边必须一致 | 两个 |
| `DB_HOST` / `DB_PORT` / `DB_USER` / `DB_PASSWORD` / `DB_NAME` | `localhost` / `5432` / 各自服务名 | 数据库连接 | 两个 |
| `REDIS_SENTINEL_ADDR` | 空 | 设了走 Sentinel 模式 | flight |
| `REDIS_MASTER_NAME` | `mymaster` | Sentinel 的 master 名 | flight |
| `REDIS_ADDR` | 空 | 单点 Redis，仅在没设 Sentinel 时生效 | flight |
| `CB_ERROR_THRESHOLD` | `5` | 窗口内多少次失败后熔断 | booking |
| `CB_TIMEOUT_SECONDS` | `30` | OPEN 持续多久后进 HALF_OPEN | booking |
| `CB_WINDOW_SECONDS` | `60` | 失败统计窗口 | booking |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`，拼错退回 `info` 并打一条 warn | booking |

`LOG_LEVEL` **目前只对 booking 生效**：flight-service 还在用标准库 `log.Printf`，没有级别概念（[D-11](./docs/tasks/D-11-structured-logging.md)）。压测时设 `warn` 关掉正常请求路径的日志——booking 的 `requestLogger` 每个请求打一行，读路径一次跑几百万请求就是几个 G。

来源：`booking-service/cmd/main.go:29-110`、`flight-service/cmd/main.go:30-91`。

## 6. 看监控

- **Grafana** <http://localhost:3000> —— 两块看板随容器 provisioning 自动加载：Services（RPS、p50/p95/p99、错误率、状态码分布）和 Infrastructure（导出器存活、PG 连接数与提交回滚速率、Redis ops/内存）。
- **Prometheus** <http://localhost:9090> —— 6 个 target，5 秒抓一次。
- **Alertmanager** <http://localhost:9093> —— 三条告警：`HighErrorRate`、`HighLatencyP95`、`ServiceDown`。

想看告警真的响一次：

```bash
./scripts/demo_alerts.sh service-down     # 停掉 flight-service，触发 ServiceDown
./scripts/demo_alerts.sh high-error-rate  # 制造错误流量，触发 HighErrorRate
./scripts/demo_alerts.sh status           # 看当前告警状态（不带参数时的默认动作）
./scripts/demo_alerts.sh restore          # 恢复
```

校验 SLI 是否达标（违反时退出码为 1）：

```bash
PROM_URL=http://localhost:9090 python3 scripts/verify_metrics.py
cat metrics-report.json
```

## 7. 跑测试

```bash
make test                              # pytest：16 个集成 + E2E 测试
go test -race -count=1 ./...           # Go 单元测试（在各服务目录下跑）
```

压测（k6，三个场景）：

```bash
make loadtest-seed          # 灌压测专用航班（不在迁移里，是测试装置）
make loadtest-steady        # 平峰：恒定 500 QPS 混合，阈值 p95<50ms、错误率<1%
```

找容量拐点要跑两遍，**先闭环摸底再开环突破**：

```bash
make loadtest-write-recon                 # 闭环加 VU，读出吞吐平台 X_max
make loadtest-write-ladder RATE_MAX=654   # 开环按 X_max 铺阶梯，0.5×–1.5×
```

`read` 场景同理（`loadtest-read-recon` / `loadtest-read-ladder`）。每次跑完直接在终端打印分档曲线并指出拐点，同一份数据存进 `k6/out/<run>.report.json`（几 KB）。**不产 CSV**——按指标采样写行的话，读路径一次跑就是 8.4 GB。

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `BASE_URL` | `http://localhost:8080` | 被测入口 |
| `SCENARIO` | `steady` | `steady` / `read` / `write` |
| `MODE` | `ladder` | `recon` 闭环摸底 / `ladder` 开环阶梯 |
| `RATE` | `500` | `steady` 的恒定速率 |
| `P95_MS` | `50` | `steady` 的 p95 阈值（ms） |
| `RATE_MAX` | 推算兜底 | `ladder` 的阶梯中心，**应填 recon 实测的 `X_max`** |
| `VU_MAX` | 读 400 / 写 200 | `recon` 的 VU 上限 |
| `STEPS` / `STEP_DURATION` / `RAMP_DURATION` | 6 / 45s / 10s | 阶梯形状 |
| `MAX_VUS` | 1000 | 开环的 VU 池上限；不够大就会变成新天花板 |

调阶梯形状用 `K6_EXTRA`：`make loadtest-read-recon K6_EXTRA="-e STEPS=4 -e STEP_DURATION=30s"`

**CI 里的 k6 不是容量门禁**：runner 规格与开发机差一个数量级，判不了容量，只跑 `steady` 烟雾档。容量结论只能来自本机，写进 `docs/reports/load/` 并记录机器配置。

## 8. 出问题时

**服务起不来 / 端口占用**：`docker compose ps` 看谁没起来，`docker compose logs -f booking-service` 看日志。5433、5434、9090、3000 这几个端口常和本机已有服务冲突。

**接口全返回 503**：熔断器 OPEN。可能是 flight-service 真的挂了（查 `docker compose logs flight-service`），也可能只是刚才连着请求了几个不存在的航班——业务错误被误计入失败统计，见 [D-01](./docs/tasks/D-01-circuit-breaker-error-classification.md)。两种情况都是默认 30 秒后自己试探恢复。

**gRPC 报 `UNAUTHENTICATED`**：两个服务的 `AUTH_API_KEY` 不一致。

**改了数据想重来**：`make stop` 会连数据卷一起删，下次启动重新灌种子数据。

**改了 `proto/flight/flight.proto`**：必须 `make proto` 重新生成，不能手改 `*.pb.go`。
