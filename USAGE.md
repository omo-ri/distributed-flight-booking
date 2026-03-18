# 使用指南

## 架构

```
Client ──HTTP──▶ booking-service (:8080) ──gRPC──▶ flight-service (:50051)
                       │                                  │
                  booking-db (:5434)               flight-db (:5433)
```

## Makefile 命令

| 命令 | 说明 |
|------|------|
| `make run` | 一键构建、启动所有服务、等待就绪、运行全部测试 |
| `make stop` | 一键停止所有服务并删除数据卷 |
| `make up` | 构建镜像并在后台启动所有服务 |
| `make down` | 停止所有服务并删除数据卷 |
| `make simple-up` | 启动服务（不重新构建镜像，适合代码未改动时） |
| `make test` | 仅运行 pytest 测试（需服务已启动） |
| `make proto` | 重新生成 gRPC protobuf 代码 |

## REST API

Base URL: `http://localhost:8080`

---

### 搜索航班

```
GET /flights?origin={origin}&destination={destination}&date={date}
```

| 参数 | 必填 | 说明 |
|------|------|------|
| origin | 是 | 出发机场 IATA 代码（如 `SVO`） |
| destination | 是 | 到达机场 IATA 代码（如 `LED`） |
| date | 否 | 日期，格式 `YYYY-MM-DD` |

```bash
curl "http://localhost:8080/flights?origin=SVO&destination=LED&date=2026-04-01"
```

返回 `200`：航班数组

```json
[
  {
    "id": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
    "flight_number": "SU1234",
    "airline": "Aeroflot",
    "origin": "SVO",
    "destination": "LED",
    "available_seats": 180,
    "price": 15000,
    "status": "SCHEDULED"
  }
]
```

---

### 获取航班详情

```
GET /flights/{id}
```

```bash
curl http://localhost:8080/flights/a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11
```

| 状态码 | 说明 |
|--------|------|
| 200 | 成功，返回航班对象 |
| 404 | 航班不存在 |

---

### 创建预订

```
POST /bookings
Content-Type: application/json
```

```bash
curl -X POST http://localhost:8080/bookings \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "550e8400-e29b-41d4-a716-446655440000",
    "flight_id": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
    "passenger_name": "Ivan Ivanov",
    "passenger_email": "ivan@example.com",
    "seat_count": 2
  }'
```

| 字段 | 类型 | 说明 |
|------|------|------|
| user_id | UUID | 用户 ID |
| flight_id | UUID | 航班 ID |
| passenger_name | string | 乘客姓名 |
| passenger_email | string | 乘客邮箱 |
| seat_count | int | 座位数（≥1） |

返回 `201`：

```json
{
  "id": "d4e5f6...",
  "user_id": "550e8400...",
  "flight_id": "a0eebc99...",
  "passenger_name": "Ivan Ivanov",
  "passenger_email": "ivan@example.com",
  "seat_count": 2,
  "total_price": 30000,
  "status": "CONFIRMED",
  "created_at": "2026-03-17T12:00:00Z"
}
```

| 状态码 | 说明 |
|--------|------|
| 201 | 预订成功 |
| 400 | 参数错误（如 seat_count < 1） |
| 404 | 航班不存在 |
| 409 | 座位不足 |

---

### 获取预订详情

```
GET /bookings/{id}
```

```bash
curl http://localhost:8080/bookings/{booking_id}
```

| 状态码 | 说明 |
|--------|------|
| 200 | 成功，返回预订对象 |
| 404 | 预订不存在 |

---

### 查询用户预订列表

```
GET /bookings?user_id={user_id}
```

```bash
curl "http://localhost:8080/bookings?user_id=550e8400-e29b-41d4-a716-446655440000"
```

返回 `200`：预订数组

---

### 取消预订

```
POST /bookings/{id}/cancel
```

```bash
curl -X POST http://localhost:8080/bookings/{booking_id}/cancel
```

| 状态码 | 说明 |
|--------|------|
| 200 | 取消成功，座位已归还，status 变为 `CANCELLED` |
| 404 | 预订不存在 |
| 409 | 已取消，不可重复操作 |

---

## 测试

15 个自动化测试用例，覆盖全部 API 端点：

| 分组 | 用例数 | 内容 |
|------|--------|------|
| 航班查询 | 5 | 搜索（带/不带日期）、不同路线、获取详情、404 |
| 预订创建 | 4 | 正常创建、座位扣减验证、座位不足 409、航班不存在 404 |
| 预订查询 | 3 | 获取详情、列表查询、404 |
| 预订取消 | 3 | 正常取消、重复取消 409、座位归还验证 |

```bash
pip install pytest requests
make test        # 仅测试
make run         # 启动 + 测试一条龙
```

## 种子数据

数据库启动时自动写入以下航班：

| flight_number | 路线 | 座位数 | 票价 |
|---------------|------|--------|------|
| SU1234 | SVO → LED | 180 | 15000 |
| SU5678 | SVO → LED | 120 | 12000 |
| DP402 | VKO → LED | 189 | 8000 |