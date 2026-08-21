# 代码结构与日志设计

> **这份文档是推演层**（`CLAUDE.md` § 3）：它回答"为什么是这个设计""什么条件下它会失效"，不复述规则——规则的唯一权威是 `CLAUDE.md`。
>
> **落地排期不在这里**：按什么顺序做、每一步做完算什么，见 [`plans/code-structure-rollout.md`](../plans/code-structure-rollout.md)（意图层）与 `docs/tasks/` 的 `R-xx` 卡片。本文只解释这些改动**为什么**长这样。

---

## 序：问题不是"文件太长"

这份文档起源于一个具体的挫败感：**打开自己的代码库，不知道从哪开始写。**

这个感觉通常会被归因成"代码太乱、文件太长、没有拆分"。但先看数字：

| 文件 | 行数 |
|---|---|
| `booking-service/internal/handler/booking.go` | 237 |
| `booking-service/cmd/main.go` | 222 |
| `booking-service/internal/service/booking.go` | 211 |
| `flight-service/internal/handler/flight.go` | 148 |
| `flight-service/internal/service/flight.go` | 134 |

没有一个失控。两百行的文件不会让人看不懂——**看不懂的是两百行里装了三件事，而文件名只说了其中一件。**

真正的病灶有两个，它们互相咬合：

1. **依赖方向是从下往上的。** 数据库定义了类型，业务层被迫接受，入口层跟着一起知道数据库长什么样。于是"改一个字段"要动三层，"想知道这层能干什么"必须读完实现。
2. **没有声明面。** 想知道 `cache` 能做什么，只能去读 163 行的 `redis.go`；想知道 service 提供什么能力，得从 211 行里挑出六个方法签名。**没有任何一个二十行的文件能回答"我手上有什么工具"。**

日志的困惑——"我要打一条 Info 该写在哪"——是这两个病灶的**症状**，不是独立问题。因为日志写在哪，完全由分层决定：一个字段该在哪一层挂进去，取决于那一层的职责边界在哪，而现在边界是糊的。

所以这份文档的顺序是：先立依赖方向，再定声明面，然后错误，最后日志。到讲日志的时候，它会变成一道没什么可争的题。

---

## 一、依赖方向：一切的根

### 现状：箭头指错了

三段代码，同一个病。

**第一段** —— `booking-service/internal/service/booking.go:39-46`：

```go
type BookingService interface {
    CreateBooking(ctx context.Context, req CreateBookingInput) (repository.BookingRow, error)
    GetBooking(ctx context.Context, id string) (repository.BookingRow, error)
    ListBookings(ctx context.Context, userID string) ([]repository.BookingRow, error)
    CancelBooking(ctx context.Context, id string) (repository.BookingRow, error)
}
```

业务层的对外契约，返回的是**数据库行**。所以 handler 必须 `import repository`（`handler/booking.go:15` 确实这么做了）。数据库加一列、改个类型，涟漪一路推到 HTTP 响应。

更微妙的是：**领域模型根本不住在 service，它住在 repository。** service 只是转手。所以"模型和 service 混在一起"这个感觉其实说轻了——模型压根没有自己的家。

**第二段** —— 同一个文件 `:83, :97, :111-117`：

```go
if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
    return FlightInfo{}, ErrFlightNotFound
}
```

业务层在判 gRPC 状态码。gRPC 是**传输细节**——今天它是 gRPC，明天换成 HTTP 调用或者消息队列，业务规则一个字都不该变，但这段代码会全线崩掉。`:184` 的 `protoToFlightInfo` 是同一个问题的另一面：protobuf 类型的翻译住在业务层里。

**第三段** —— `flight-service/internal/service/flight.go:19-26`：

```go
type flightService struct {
    repo  *repository.FlightRepo
    cache *cache.RedisCache // nil = caching disabled
}
```

依赖的是**具体类型**，而"没配缓存"用 `nil` 表达。后果是 `if s.cache == nil` 在四个方法里出现了六次（`:30, :35, :51, :62, :65, :81`），而且 `:38, :52` 直接调用 `cache.MarshalJSON`——**缓存的序列化格式泄漏进了业务逻辑**。

这三段的共同点：**内层知道外层的事**。业务规则知道数据库的表结构、知道 gRPC 的状态码、知道缓存用 JSON 编码。这就是"读不懂"的根源——你想理解一条业务规则，必须同时理解三种技术细节。

### 目标：箭头全部指向内

```
handler ──▶ service ──▶ domain ◀── repository
   │                       ▲            │
   └───────────────────────┘            │
                    cache ──────────────┘
                grpcclient ─────────────┘
```

三条规则就够了：

**1. `internal/domain` 只放纯数据类型，不依赖任何东西。**

```go
package domain

type Booking struct {
    ID             string
    UserID         string
    FlightID       string
    PassengerName  string
    SeatCount      int32
    TotalPrice     int64
    Status         BookingStatus
}
```

没有 import（除了标准库），没有方法调用外部，没有 tag。它是这个系统里**唯一不会因为技术选型变化而变化**的东西。判断一个类型该不该进 domain：换掉 Postgres、换掉 gRPC、换掉 Redis，它还在吗？

**2. service 声明它需要什么，不声明谁来实现。**

```go
package service

// 对外提供什么
type BookingService interface {
    CreateBooking(ctx context.Context, in CreateBookingInput) (domain.Booking, error)
    CancelBooking(ctx context.Context, id string) (domain.Booking, error)
    // ...
}

// 需要什么工具
type BookingRepo interface {
    Create(ctx context.Context, b domain.Booking) (domain.Booking, error)
    GetByID(ctx context.Context, id string) (domain.Booking, error)
    UpdateStatus(ctx context.Context, id string, s domain.BookingStatus) error
}

type FlightGateway interface {
    GetFlight(ctx context.Context, id string) (domain.Flight, error)
    ReserveSeats(ctx context.Context, flightID string, n int32, bookingID string) error
    ReleaseReservation(ctx context.Context, bookingID string) error
}
```

**接口写在用它的那一层，不写在实现它的那一层。** 这是整套设计里最容易写反的一条。Go 的惯例是"接受接口、返回结构体"，而接口的定义权属于消费方——因为只有消费方知道自己需要什么。`repository` 包不该定义 `BookingRepo`，它只是碰巧满足了这个接口。

**3. 适配器负责翻译，翻译不外泄。**

`repository` 把数据库行翻成 `domain.Booking`，`grpcclient` 把 pb 消息翻成 `domain.Flight`、把 gRPC 状态码翻成领域错误，`cache` 自己决定用什么编码。service 从此见不到 `codes.NotFound`，也见不到 `json.Marshal`。

### 判据：不是所有依赖都要倒置

这一点必须说清楚，否则倒置会变成新的灾难——满屏接口，每个只有一个实现，读代码要跳三次才找得到真正干活的地方。

**判据是：这个依赖会不会因为环境不同而需要替换？**

| 依赖 | 会替换吗 | 结论 |
|---|---|---|
| `repository` | 会（Postgres ↔ 内存 fake） | 倒置 |
| `cache` | 会（Redis ↔ 内存 fake ↔ 不启用） | 倒置 |
| `FlightGateway` | 会（真 gRPC ↔ 契约桩） | 倒置 |
| `logctx` | **不会**——任何环境下都是同一个 context 字段袋 | **不倒置，直接 import** |
| `uuid.New()` | 不会（除非要测确定性 ID，那时再说） | 不倒置 |

给不会被替换的东西抽接口，付出的是真实成本（每层多一个字段、组装多一根线、测试多一个 mock、读代码多一次跳转），换来的是零。**"到处是抽象但看不懂"的代码，就是这么来的。**

> **现状差距**
>
> 两个服务都没有 `domain` 包。service 层有接口但返回持久化类型；repository / cache / grpcclient 全是具体类型。
> 已登记：`T-08`（cache 抽接口）、`T-09`（内存 fake）——方向一致，但描述是按旧结构写的：T-08 由 [`R-09`](../tasks/R-09-flight-service-ports.md) 取代，T-09 在其之后重写。

---

## 二、包与文件：`ports.go` 是包的目录

依赖方向定了，文件怎么放就有了判据。

先排除一个错误动机：**拆文件不是为了让文件变短。** 前面那张表说明行数没有失控，按用例拆成六个文件只会得到四个二十行的碎片，然后你依然不知道这个包能干什么。

真正要解决的是：**打开一个包，第一眼看到什么。** 现在第一眼看到的是 `SearchFlights` 的实现细节。

### 目标布局

```
internal/
├── domain/
│   ├── booking.go          实体 + 状态枚举
│   ├── flight.go
│   └── errors.go           领域错误（见第三章）
├── service/
│   ├── ports.go            ★ 包的目录：对外提供什么 + 需要什么
│   ├── booking.go          用例实现
│   └── errors.go           service 特有的错误（如果有）
├── handler/
│   ├── booking.go          处理函数
│   └── errors.go           领域错误 → HTTP/gRPC 码的映射表
├── repository/             适配器
├── cache/                  适配器
├── grpcclient/             适配器
└── app/                    组装（T-01）
```

`ports.go` 是这套布局的核心。它二十来行，讲清楚这一层的**全部输入输出**，是你写新代码时**唯一需要先读的文件**。加一个用例，先看 `ports.go` 知道手上有什么工具；改一个依赖，先改 `ports.go` 再改实现。

`errors.go` 独立出来的理由类似：错误是**跨层契约**，散在实现里就没法一眼看全（现在 booking 的 sentinel 埋在 `booking.go:18-23`，混在业务代码中间）。

### 拆分阈值：按"能不能独立说清"判，不按行数判

一个文件应该装一件**能独立讲清楚的事**。到了 250 行还讲得清，就不用拆；讲不清了，说明里面装了两件事——**那才是拆的理由，而且拆的边界是那两件事的分界，不是行数的中点。**

按这条判据，现在的 `handler/booking.go`（237 行）该拆：它装了处理函数 + 错误映射 + 参数解析三件事，拆出 `errors.go` 之后剩下的就讲得清了。而 `service/booking.go`（211 行）拆掉 sentinel 之后是六个用例，讲得清，暂时不用动。

> **现状差距**
>
> 两个服务都没有 `ports.go` 和 `errors.go`；`internal/app` 不存在（`T-01`，critical，`blocks: [T-02, D-03]`）。

---

## 三、错误：按"接收方还需要知道什么"选形式

### 现状：两个服务两套做法

**booking 是健康的**（这是全项目错误处理最干净的地方，值得作为样板）：service 定义 sentinel（`booking.go:18-23`），handler 用 `errors.Is` 映射成 HTTP 码。

**flight 是坏的**——`handler/flight.go:48, :67`：

```go
row, err := h.svc.GetFlight(ctx, req.GetId())
if errors.Is(err, repository.ErrNotFound) {
    return nil, status.Error(codes.NotFound, "flight not found")
}
```

入口层**跨过业务层**，直接匹配数据库层的错误。flight 的 service 一个错误都没定义，于是 handler 只能越级去认 repository 的。

同一个文件 `:32, :52` 还有：

```go
return nil, status.Errorf(codes.Internal, "search flights: %v", err)
```

**把内部错误原文当成 gRPC 消息返给调用方**——数据库连接串、表名、约束名，全都随着错误消息出去了。这是已登记的 `D-20`。

### 目标：翻译只发生在边界，方向单一

依赖方向定了之后，这部分是**推导出来的，没有选择余地**：

```
        入站适配器                                    出站适配器
  (handler: HTTP / gRPC)                    (repository / cache / grpcclient)

  domain error ──▶ 状态码 + 泛化消息          驱动错误 / 传输错误 ──▶ domain error
       ▲                                                    │
       └────────────── service 只见 domain error ◀──────────┘
```

- **出站适配器**把 `pgx.ErrNoRows`、`codes.NotFound`、`redis.Nil` 翻成领域错误
- **入站适配器**把领域错误翻成 HTTP 状态码或 gRPC 码，**并且只返回泛化消息**——原文只进日志（这就是 `D-20` 的修法）
- service 两头都不认识：它不知道 `codes` 包存在，也不知道 `pgx` 存在

### 领域错误长什么样：混合，按一条判据分

**判据：这个错误的接收方，除了"哪一类错"之外还需要知道别的吗？**

**不需要 → sentinel。** 简单、Go 惯用、`errors.Is` 匹配：

```go
var (
    ErrNotFound         = errors.New("not found")
    ErrAlreadyCancelled = errors.New("booking already cancelled")
)
```

"订单不存在"没有更多可说的，接收方拿到这个就够了。

**需要 → 错误类型。** `errors.As` 取出结构化数据：

```go
type InsufficientSeatsError struct {
    Requested int32
    Available int32
}

func (e InsufficientSeatsError) Error() string {
    return fmt.Sprintf("insufficient seats: requested %d, available %d", e.Requested, e.Available)
}
```

座位不足是这个系统里最高频的业务拒绝，而"还剩几个"既是调用方想知道的，也是排障时想在汇总行里看到的：`reason=insufficient_seats` 后面跟一个 `available=1`。如果只用 sentinel，这个数字就只能靠 `fmt.Errorf` 拼进字符串——**日志字段挂不上，API 响应也拼不出**。

不要走极端：全用 sentinel 会在最需要细节的地方失效；全用类型会让 `ErrNotFound` 这种零数据的错误也背上一个 struct 和一个 `Error()` 方法，十来个错误就是一百多行纯样板。

### 这条判据的形状

留意它和你已有的两条判据是同一个模子：

| 决定什么 | 判据 |
|---|---|
| 日志级别 | 谁需要看 |
| 指标标签 | 取值有没有上界 |
| 错误形式 | 接收方还需要知道什么 |

**都是先问需求，再选形式。** 反过来（先选一个"标准做法"再往上套需求）就是过度设计的起点。

> **现状差距**
>
> flight 的 service 无错误定义，handler 越级匹配 `repository.ErrNotFound`（`handler/flight.go:48, :67`）；内部错误原文外泄（`:32, :52`，即 `D-20`）。
> booking 的 sentinel 位置对但埋在业务文件里，需要迁进 `errors.go`；错误类型（`InsufficientSeatsError`）尚未存在。

---

## 四、日志：为什么你没有 logger

现在可以回答最初那个问题了。

### 核心：在请求路径上，你不"写日志"

这是整套设计最反直觉的一点，也是那个挫败感的直接来源：

> **handler、service、适配器都不持有 logger。这是故意的。**

你在请求路径上做的不是"打一条日志"，而是**往这次请求的字段袋里挂东西**。那一条日志由入口统一写出——HTTP 是中间件，gRPC 是拦截器链的最外层。

级别也不是你选的。**级别由这次请求怎么结束决定。**

为什么这么设计，一句话：改造前一次 `POST /bookings` 产出 **11 行**日志（handler 2 + service 6 + 中间件 1 + flight 侧 2），它们描述的是**同一件事**，而"多快、多少、成功率"这三类问题指标已经答过了。改造后是 **3 行**（booking 1 + flight 2，因为一次下单是两次 RPC），每行带全字段。**排障时你要的是"这一条请求发生了什么"，一行带全字段的汇总行比八行流水更快读懂。**

### 所以"我要打一条 Info"翻译成什么

**① 我想记一个事实**

```go
logctx.Add(ctx, logctx.KeyBookingID, bookingID)
```

不涉及级别。请求成功结束，入口自然打 `Info`——**Info 是默认结果，你什么都不用做。**

**② 出了岔子，但我自己处理了**（座位释放失败不挡取消、缓存写失败不挡返回、重试后成功）

```go
logctx.Degraded(ctx, "release_failed")
```

这就是"打一个 Warn"。但注意措辞：你声明的是**「发生了降级」这个事实**，`Warn` 是它的后果，不是你的选择。

**③ 业务拒绝**（座位不足、订单不存在、参数非法）

```go
// service：只管返回错误，不碰日志
return domain.InsufficientSeatsError{Requested: 3, Available: 1}

// handler：翻译成状态码时顺手登记
logctx.Reject(ctx, "insufficient_seats")
return c.JSON(http.StatusConflict, ...)
```

级别恒为 **`Info`**。这条最容易写错——绝大多数人会打 `Warn`。但**没人需要为一次座位不足做点什么**，把它打成 `Warn` 会让"有多少事真的需要人看"这个信号失真。这和压测错误率把 409 算进失败是同一个错误。

**④ 内部错误**

```go
// service：往上 wrap，wrap 链本身就是调用路径
return fmt.Errorf("create booking: %w", err)

// handler
logctx.Fail(ctx, err)
return c.JSON(http.StatusInternalServerError, api.Error{Message: "internal error"})
```

级别 `Error`——**不是你定的**，是入口看到 `status >= 500` 自己判的。返回给调用方的是泛化消息，`err` 原文只进日志。

**最终产出一行：**

```json
{"level":"WARN","msg":"request","trace_id":"a3f...","route":"/bookings/:id",
 "method":"DELETE","status":200,"latency_ms":43,
 "booking_id":"...","degraded":"release_failed"}
```

### 请求路径之外：这时你才真的写日志

不属于任何一条请求的事，**才用真正的 logger 直接打**：

```go
log.Info("config loaded", "http_port", port, "db_host", host, ...)   // 一次进程一条，含全部配置值，不含密码
log.Warn("circuit breaker state changed", "from", "closed", "to", "open", "error_count", 5)
```

**判据：这件事能不能归给某一条请求？** 能就挂字段袋，不能就独立成行。

熔断打开影响的是它**之后的所有请求**，埋进任何一条里都找不到——排障的起点常常就是它。同理还有启动分步、配置加载。

### 一张表

| 你想干的事 | 实际写法 | 级别谁定 |
|---|---|---|
| 记个事实 | `logctx.Add(...)` | 不涉及 |
| 打 Warn | `logctx.Degraded(ctx, "什么降级了")` | 封装内部 |
| 业务拒绝 | `logctx.Reject(ctx, "原因")` | 恒为 Info |
| 内部错误 | `logctx.Fail(ctx, err)` | 入口按状态码 |
| 组件 / 启动事件 | `log.Info` / `log.Warn` 直接打 | 你自己定 |

### 为什么需要这层语义化封装

现在的代码里，"降级"这件事要写两行：

```go
logctx.Escalate(ctx, slog.LevelWarn)
logctx.Add(ctx, logctx.KeyDegraded, "release_failed")
```

**这个两行惯用法在仓库里抄了五遍**：`service/booking.go:171-172`、`service/flight.go:127-128`、`cache/redis.go:78-79`、`cache/redis.go:109-110`、`grpcclient/flight.go:116-117`。

而它可以**只写对一半**：忘了 `Escalate`，降级就静默停留在 `Info`。一次座位释放失败、一次缓存写失败，从此在 `LOG_LEVEL=warn` 的压测里完全消失——而代码看起来完全正常。**编译器抓不到，评审也很容易放过，出错概率随抄写次数线性增长。**

把 `Escalate` 收进 `Degraded()` 内部之后，"忘了抬级别"不再可能发生，因为抬级别不再是调用方的责任。而且这四个函数名恰好对应级别规则的四行——**代码和规范一一对应，不用记表也不会写错。**

### 字段袋的写入语义：按字段定，不按包定

`logctx.go:93-101` 的 `set()` 是**同名 key 覆盖**。这带来一个真 bug：

```go
// flight 一次 ReserveSeats 里可能先后发生：
logctx.Add(ctx, KeyDegraded, "cache_set_failed")                // redis.go:79
logctx.Add(ctx, KeyDegraded, "search_cache_invalidate_failed")  // flight.go:128
// 汇总行里只剩后一个
```

`Escalate` 只升不降，所以**级别是对的**（还是 Warn），但**原因丢了一半**。这是最坏的一种 bug：不报错、不改级别、只是让排障时看到的因果链是残缺的。

关键在于——**覆盖本身不是错的**。`retries` 要的就是最终次数、`cb` 要的就是最终状态、`error` 要的就是最外层那个。错的是**用一套写入语义套所有字段**。

| 语义 | 适用字段 | 理由 |
|---|---|---|
| 覆盖（默认） | `retries`、`cb`、`error`、`cache`、业务字段 | 要的是最终值 |
| 累加去重 | `degraded` | 要的是完整清单 |
| 首写胜出 | `reason` | 第一个拒绝原因是真原因，后面的都是它的后果 |

累加输出成逗号串而不是 JSON 数组：

```json
"degraded":"release_failed,cache_set_failed"
```

理由在下一章——LogQL 里 `|= "release_failed"` 直接命中，数组还得多绕一层。

这条也**反过来证明了封装是必须的**：累加语义不可能留给调用点（"先读出来、拼上、去重、再写回"没人会每次写对）。

### panic：两个严重程度完全不同的缺口

**booking 侧**——`cmd/main.go:109-115` 的注册顺序是 `RequestID → requestLogger → Recover`，Echo 里注册顺序即执行顺序，所以 `Recover` 在 `requestLogger` **内层**。panic 被 recover → `next(c)` 返回 error → 汇总行照打、`status=500` → 判 `Error`。这部分是对的。

**但堆栈不在那一行里。** Echo 的 `Recover` 用的是它自己的 logger（非 slog、非 JSON），堆栈被打到另一路输出。接了 Loki 之后这条更难受：那是**非 JSON 的多行文本**，`| json` 解析不了、`level` 标签提不出来——它会变成一坨没有标签、无法关联 `trace_id` 的孤儿行。

**flight 侧严重得多**——`cmd/main.go:127-132` 的拦截器链是 `logging → metrics → auth`，**没有 recovery**。grpc-go 默认不 recover panic，所以：

> flight-service 里任何一次 panic = **整个进程崩溃**，而且那条 RPC 的汇总行**永远不会写**（拦截器的写日志在 `handler()` 返回之后，panic 直接从那里穿过去了）。

在 Loki 里看到的是：流量正常 → 突然全断 → 容器重启后恢复，**中间什么都没有**。

**目标形态**，三条钉死：

1. **顺序**：recovery 必须在 logging 之内、其余一切之外。在外面汇总行丢失，在里面太深则 metrics / auth 的 panic 抓不到
2. **`logctx.Fail(ctx, panicErr)` + 强制附加 `stack` 字段**，由入口按 500 / `Internal` 判成 `Error`。`stack` 进白名单表，遵循"非常态才出现"原则（和 `cb`、`retries` 同类）
3. **加 `panics_total` counter**——没有指标的机制等于不存在

### trace_id：外部输入不能照单全收

`booking-service/cmd/main.go:109` 用 `middleware.RequestID()`，语义是**优先采信外部传入的 `X-Request-ID`**；`flight-service/internal/logging/interceptor.go:93` 同样直接采信 metadata 里的 `x-trace-id`。两处都**没有任何校验**。

先排除一个不成立的担心：**日志伪造不成立**。`slog.NewJSONHandler` 会转义换行，塞 `\n{"level":"error"...}` 进去只会得到一个带转义符的字符串字段。

真实的问题不需要有攻击者：**任何一个客户端固定传同一个 `X-Request-ID`，所有请求就共用一个 trace_id**——k6 脚本里手滑写死一个 header 就够了。接了 Loki 之后，点 derived field 想看"这一条请求发生了什么"，拉出来的是几十万条不相干的行，**而且这个失效是静默的**。附带的还有长度不受控：超大 header 变成超大日志行，Loki 有 `max_line_size` 上限，超了**整行丢弃**。

**目标**：按 32 位十六进制校验，不合法就丢弃并自己生成。这个格式不是随便挑的——`interceptor.go:98` 现在自生成的就是 16 字节 hex，而它**正好是 W3C Trace Context 的 trace-id 格式**（见第五章的升级路径）。

### 组件状态行的级别：现有规则在这里失灵

`breaker.go:125-127` 照着规则实现：熔断**翻开**打 `Warn`，**恢复**打 `Info`。叠加上"容量基线压测用 `LOG_LEVEL=warn`"，结果是：

> 压测日志里，你会看到熔断打开，然后**永远看不到它关闭**——一个没有终点的状态机轨迹。

而压测恰恰是最容易把熔断打开的场景。事后翻日志只能看到"它开了"，"开了多久""什么时候恢复的"全部消失。指标能补一部分（状态 gauge），但分辨率是抓取间隔，而**"故障持续了多久"是复盘报告的核心数字**。

**目标：组件状态行一律 `Warn`，不分翻开/恢复。**

正当性在于：**常态是"不迁移"**。一个健康运行的系统，熔断器一天迁移 0 次，任何一次迁移都是非常态信号——包括恢复，因为"它恢复了"回答的是"刚才那次故障持续了多久"。

更一般地：**"级别按 outcome 判"这条判据是为请求路径设计的，套到组件状态行上就失灵了。** 请求的 outcome 有"成功 / 拒绝 / 失败"的真实语义差异；而状态迁移的两端是**同一个人在同一个场景下要看的同一件事**——你不会只关心它开、不关心它关。把它们劈成两个级别，等于让一个可能被关掉的开关切掉状态机轨迹的一半。

（这条要改 `CLAUDE.md` § 4，走 PR。）

### 探针端点

`booking-service/cmd/main.go:145` 把 `/metrics` 排除在汇总行外，理由是"观测面自己的流量"。`D-05` 加 `/livez` `/readyz` 之后，K8s 每几秒探一次，**必须按同一条判据一起排除**，否则 Loki 里九成的行是探针。

但**探针失败要打行**——按上一节的原则，那是组件状态变更（且会导致容器重启），`Warn`。

> **现状差距**
>
> 五条独立改动，互不依赖，也不依赖分层重构：
> ① `logctx` 语义化封装（`Degraded` / `Reject` / `Fail`，原语转私有）
> ② `degraded` 累加去重 + `reason` 首写胜出
> ③ 两侧 panic recovery + `stack` 字段 + `panics_total`（flight 侧优先，它现在是进程崩溃）
> ④ trace_id 32-hex 校验（两侧各改一处）
> ⑤ 组件状态行改 `Warn`（需先改 `CLAUDE.md`）
>
> 另有一处耦合问题记账：`breaker.go:129` 用 `slog.Default()` + `context.Background()`，绕过了注入的 logger，测试要断言这行就得替换全局。

---

## 五、这套设计什么时候会失效

推演层文档的义务：说清楚边界在哪。

### "一次请求一条汇总行"的盲区：请求结束前，它不存在

汇总行在 `next(c)` / `handler()` **返回之后**才写。所以一条请求在它结束之前，日志里完全不存在。

叠加 `D-25`（**没有任何超时**，还是 `todo`）：一条请求可以**永远**卡住，它的汇总行**永远不会被写出来**。再叠加 `D-03`（无优雅停机）：SIGTERM 时在途请求被直接砍断，同样一行都没有。

结果是——**"服务 hang 住了"这个最需要日志的现场，恰恰是日志唯一完全空白的现场。**

**这个洞不该用日志补。** 加"慢请求中途行"会破坏"一条请求 = 一行"这个心智模型（同一条请求出现两行，按 trace_id 查会拿到两条），而它想回答的"现在有多少请求卡着、卡了多久"本来就是 gauge 的问题。

正确的解法是两条：

- **修 `D-25`**——有了超时，盲区长度 = 超时时长，卡住的请求会以 `DeadlineExceeded` 收尾并正常吐出汇总行
- **加 in-flight gauge** + "in-flight 持续高于 N"的告警

所以：**`D-25` 不只是一个可用性缺陷，它是日志可观测性的前置。** 可观测性的能力上限，常常是被别的缺陷卡住的。

### 服务数上到 5+ 时，自建 trace_id 会不够用

现在不上 OpenTelemetry 是对的：otel 的价值在接上 trace 后端之后（火焰图、span 树、跨服务耗时归因），而这套东西一个都没有。只为了"让两行日志能 join"引入 SDK + 两个 instrumentation 包，是拿大依赖换二十行代码能做到的事。

**什么条件下这个判断翻转**：调用链有分叉、跳数超过 3、或者需要回答"这次请求的 800ms 花在哪一段"。现在只有两跳（booking → flight），span 树能提供的信息，两条带 `latency_ms` 的汇总行几乎全给了。

**升级路径是通的且代价为零**：trace_id 已经是 32 hex，与 W3C trace-id 同形，将来换的只是"谁生成它"，字段名和值的形状都不用动。

> 顺带记一笔矛盾：`roadmap.md:108` 把 otel + Tempo 排在阶段 1 第 3 步，而 `engineering.md` § 5 有一整段"为什么不上 OpenTelemetry"。**这两段不能同时为真**，等真做到那一步再修。

### 不采样的代价，和它为什么仍然划算

采样是**第三个观测面**：既不是完整日志也不是指标。采出来的 1% 回答不了"这一条具体发生了什么"（你要查的那条大概率没被采中），也回答不了"多少"（指标已经全量）。

高负载时切 `LOG_LEVEL=warn` 就够——那时剩下的恰好是压测中你真正想看的东西。**判据不是"打不打"，而是"关得掉吗"。**

（这条有学费：一次读路径压测 385 万请求 × 每请求至少 2 行 = 770 万行、几个 G，当时两个服务都没有级别开关。）

---

## 六、日志出了这个进程之后：Loki

前五章讲的是"日志在进程里怎么长出来"。这一章讲它离开进程之后去哪。

### 采集：Alloy 从 docker socket 读 stdout

```
应用 stdout ──▶ docker json-file ──▶ Alloy ──▶ Loki ──▶ Grafana
```

两个服务都只写 stdout（`cmd/main.go:32` / `:34` 的 `slog.NewJSONHandler(os.Stdout, ...)`），`docker-compose.yml:6-9` 全站统一 `json-file` driver + `max-size: 50m` 轮转。**stdout 已经是既成的采集面**，所以采集器只要来读就行，应用零改动。

采集器选 **Alloy 不选 Promtail**，两个理由：Promtail 已经 EOL，Grafana 官方继任是 Alloy；更重要的是**形态在阶段 2 不变**——Alloy 在 compose 里是个容器，在 K8s 里是 DaemonSet，中间那层"应用只管往 stdout 写、采集器负责搬运"的契约一行都不用改。

被否掉的两条：Docker 的 `loki` logging driver（是插件、缓冲弱、会让 `docker compose logs` 失效、上 K8s 时整个作废）、应用内 slog handler 直推（业务进程与观测后端耦合，进程崩溃时缓冲区里那批最想看的日志一起没）。

**要记的账**：挂 `/var/run/docker.sock` 进容器 = 给了 Alloy **宿主机 root 等价权限**。本地栈可接受，阶段 2 上 K8s 换成 DaemonSet 读节点日志就不存在这个面。

### 标签：只有三个

**Loki 的标签是索引，每个唯一的标签值组合是一条独立的流。** 这里的陷阱和 Prometheus 高基数是同一类——而汇总行里正躺着 `trace_id`、`booking_id`、`user_id` 这些 UUID。

需要给"日志字段不是指标标签"这句话打个补丁：**进了 Loki 之后，一部分日志字段又变回有基数约束的东西了。**

| 标签 | 上界 | 来源 |
|---|---|---|
| `service` | 2（+ 基础设施容器） | Alloy 的 docker 服务发现 |
| `level` | 4 | **需要 `loki.process` 先 `stage.json` 解析、再 `stage.labels` 提升** |
| `stream` | 2（stdout / stderr） | Alloy 原生，零成本 |

**其余全部靠查询时 `| json` 解析**，包括 `trace_id`。

判据和指标那条一模一样，只是换了个系统：**取值集合有上界吗，上界有多大？** `route` 虽然也有界，但它的收益（少一次 JSON 解析）远小于流数量 ×8 的代价——**Loki 的 JSON 解析在这个数据量下根本不是瓶颈。**

`stream` 对两个 Go 服务基数是 1（它们只写 stdout），它真正有用的地方是 Postgres（默认写 stderr）和 Redis。

### 采集范围：全栈都采，但只有 JSON 行有 `level`

`docker-compose.yml` 里有 10+ 个容器，**只有两个 Go 服务吐 JSON**，其余是各家自己的文本格式。

选择全栈都采，理由是排障现场往往是"应用报 `Unavailable` 的同一秒，Redis Sentinel 在说什么"——两边不在同一个系统里就得回到 `docker compose logs` 手工对时间戳，那 Loki 就只做了一半。

代价是 `level` 标签"有的行有、有的行没有"，必须把语义钉死：

> `{level="error"}` 查的是**应用**错误。查基础设施用 `{service="postgres"} |= "FATAL"`。

不给 Postgres / Redis 写正则提取级别——那等于去维护三方组件的日志格式，它们升级一次就可能改。全文匹配够用，而且**这恰好是 Loki 擅长的**（不索引全文但扫描很快）。

### 保留期：15 天，且 `D-09` 是硬前置

`docker-compose.yml:189` —— Prometheus 是 `--storage.tsdb.retention.time=1h` 且**没有 volume**（`D-09`，`todo`）。指标只活一小时，容器一重启就归零。

这决定了 Loki 保留期必须和它对齐，而且顺序不能反：**联查的入口是指标曲线上的一个异常点。** 日志留 15 天而指标只留 1 小时，那 14 天 23 小时里的日志你根本**找不到入口**——不知道该查哪个时间窗。

> **`D-09` 是 Loki 的前置任务，不是并行任务。** 不修它，联查从第一天就是断的，而排障请求几乎从不在事发 1 小时内到达你手上。

存储用 filesystem + volume 就够：一次排障型压测约 2.7 GB 原始日志，Loki 压缩后约 10:1 ≈ 270 MB；日常 `info` 流量小得多。不上 MinIO——本地模拟对象存储学到的配置，真上云时会被云厂商的 IAM / 桶策略重写一遍，基本作废。

**必须显式开 compactor 的 retention。** Loki 默认 `retention_enabled: false`，**不删数据**——这和 Prometheus"给个 flag 就自动过期"的习惯完全不同，是新接 Loki 最常见的翻车点。

### 联查：两支柱，derived field 指向 Loki 自己

- Grafana 面板加 data link，从异常时间点**带着时间窗**跳进 Explore / Loki
- 日志里的 `trace_id` 配 derived field，指向 **Loki 自己**的查询：

```logql
{service=~"booking-service|flight-service"} | json | trace_id="$1"
```

一键把一次下单在**两个服务**的汇总行拉到一起。

将来 Tempo 到位时，把 derived field 的 target 换掉即可——**trace_id 值的格式不用动**（见第五章）。

### 压测：按目的分档

`LOG_LEVEL=warn` 原本的理由是"磁盘会被写满"。接了 Loki 之后这个理由**变形了**：`max-size: 50m` 已经兜住了磁盘，真正的成本转移到别处。

算一笔账：385 万请求 × 每请求 2 行 ≈ **770 万行**，一条汇总行约 250–350 字节，合计 **≈ 2–2.7 GB**。压测持续 10 分钟就是 **≈ 4 MB/s 持续写入**——而 Loki 单租户默认 `ingestion_rate_mb` 恰好就是 4 MB/s。**压测流量会正好顶在默认限流线上。**

更要命的是第二笔：Alloy 解析 770 万行 JSON + Loki 建索引压缩，**和被测服务抢同一台机器的 CPU**。而"容量结论只能来自本机压测"——这会直接污染写进 `docs/reports/load/` 的那个数字。

所以按**目的**分档，而不是用一个值服务两个互斥的目的：

| 压测类型 | `LOG_LEVEL` | Alloy | 为什么 |
|---|---|---|---|
| 容量基线 | `warn` | **停掉** | 要测量不受干扰，观测栈自己也是负载 |
| 故障演练 / 排障 | `info` | 在场，限流放开 | 要现场可回溯，不要数字 |

这把一条隐含规则显式化了：**容量基线压测时，观测栈自己也是负载。** `docs/reports/load/` 的报告模板要因此多记一行"本次压测时观测栈的状态"。

### 日志告警：只覆盖"指标不存在的信号"

Loki 自带 ruler，能对 LogQL 结果告警，写法和 Prometheus 规则几乎一样。诱惑很大，但：

```logql
sum(rate({service="booking-service", level="error"}[5m])) > 0
```

这和 `alerts.yml` 里的 `HighErrorRate` 是**同一件事的两种算法**（一个算 5xx 占比、一个算 error 行数）。两套阈值必然漂移，然后你会遇到"指标说没事、日志说有事"，而**没有第三个东西能裁决谁对**。

**边界：日志告警的正当用途是"指标不存在的信号"，不是"指标已有信号的第二种算法"。** 落在线内的具体只有三类：

- Postgres 的 `FATAL` / `PANIC`
- Redis Sentinel 的 `+switch-master`（主从切换发生的**那一刻**）
- Go panic 堆栈

**硬前置：先有断流告警。** 日志告警会**静默失效**——Alloy 挂了、Loki 拒了写入，它不是报警，是**永远不触发**，长得和"系统很健康"一模一样。

这条断流告警必须出在 **Prometheus 侧**（放 Loki 侧它自己也会一起哑）：用 Alloy / Loki 暴露的 `/metrics` 做写入速率或 `up` 判断。

**不做这条，上面三条告警就是装饰品。**

这也是整套告警体系里唯一一处引入新失效模式的地方——两条链路的跳数差得很远：

```
指标告警：  应用 /metrics ──▶ Prometheus ──▶ 规则            (1 跳)
日志告警：  应用 stdout ──▶ json-file ──▶ Alloy ──▶ Loki ──▶ ruler   (4 跳)
```

**每多一跳就多一个哑掉的地方。**

---

## 七、落地路径

**已排期，本章移出**：14 步的顺序、每步的阻塞关系与排期理由见 [`plans/code-structure-rollout.md`](../plans/code-structure-rollout.md)，每一步的验收标准见 `docs/tasks/` 的 `R-01` ~ `R-13` 与 [`T-01`](../tasks/T-01-extract-app-package.md)。

留在这里的只有一句因果：**第 0 批（日志口径的五条）不依赖分层重构，可以先做；分层倒置必须等 T-01 铺完安全网，且 flight 先于 booking**——理由分别是「测试能在进程内起真实服务」和「第一次做这种重构一定会撞到纸面上没想到的东西，用小服务当试验田返工成本低」。
