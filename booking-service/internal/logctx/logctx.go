// Package logctx 承载「一次请求一条汇总行」这个口径所需的字段袋。
//
// 口径（CLAUDE.md § 4）：正常请求路径上 handler / service / grpcclient 一行日志都不打，
// 值得记的东西挂进 context 里的字段袋，由中间件在请求收尾时一次性写进那一条汇总行。
// 这样做的结果是「下层没有 logger 可打」——不是靠人自觉遵守的约定，而是结构上的保证。
//
// 字段白名单就是本文件里的 Key* 常量，理由与全表见 docs/conventions/engineering.md。
// 新增字段先改那张表，再加常量——否则汇总行会退化成什么都往里塞的另一种噪音。
package logctx

import (
	"context"
	"log/slog"
	"sync"
)

// 固有字段：由中间件无条件写入，不走字段袋。
const KeyTraceID = "trace_id"

// 业务字段：由 handler / service 按需挂载。
const (
	KeyBookingID   = "booking_id"
	KeyFlightID    = "flight_id"
	KeyUserID      = "user_id"
	KeySeatCount   = "seat_count"
	KeyOrigin      = "origin"
	KeyDestination = "destination"
	KeyDate        = "date"
)

// 结果字段：描述这一条请求怎么结束的。
const (
	KeyOutcome = "outcome" // 目前只有 OutcomeRejected 一个取值
	KeyReason  = "reason"  // 被拒的原因，如 insufficient_seats
	KeyError   = "error"   // 内部错误的 wrap 链
)

// 韧性字段：只在异常路径上出现，字段一出现就意味着走了非常态分支。
const (
	KeyDegraded       = "degraded"        // 异常但已自动处理，如 retry_recovered
	KeyDownstreamCode = "downstream_code" // 下游返的 gRPC 码
	KeyRetries        = "retries"         // 重试次数，0 次时不挂
	KeyCB             = "cb"              // 熔断器状态，closed 时不挂
)

const OutcomeRejected = "rejected"

type ctxKey struct{}

// fields 是一次请求的字段袋。中间件建、下层写、中间件读，跨 goroutine 所以要加锁。
type fields struct {
	mu      sync.Mutex
	traceID string
	kv      []any // 交替的 key / value，与 slog 的可变参数同构
	level   slog.Level
}

// New 在 ctx 上装一个新的字段袋，基线级别是 Info。
func New(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, &fields{traceID: traceID, level: slog.LevelInfo})
}

func from(ctx context.Context) *fields {
	f, _ := ctx.Value(ctxKey{}).(*fields)
	return f
}

// TraceID 返回本次请求的 trace_id；没有字段袋时返回空串。
func TraceID(ctx context.Context) string {
	f := from(ctx)
	if f == nil {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.traceID
}

// Add 挂载 key / value 对（slog 风格的可变参数）。
//
// 同名 key 覆盖而不是追加——一条 JSON 日志里出现两个同名字段没有意义。
// 没有字段袋时（例如后台任务、单测直接调下层）静默丢弃，不 panic。
func Add(ctx context.Context, args ...any) {
	f := from(ctx)
	if f == nil || len(args) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 0; i+1 < len(args); i += 2 {
		k, ok := args[i].(string)
		if !ok {
			continue
		}
		f.set(k, args[i+1])
	}
}

// set 必须在持锁时调用。
func (f *fields) set(k string, v any) {
	for i := 0; i+1 < len(f.kv); i += 2 {
		if f.kv[i] == k {
			f.kv[i+1] = v
			return
		}
	}
	f.kv = append(f.kv, k, v)
}

// Escalate 抬高汇总行的级别，只升不降。
//
// 用它表达「这一条请求发生过异常但已自动处理」（Warn）。5xx 不用它——
// 中间件按响应状态码直接判 Error，无需下层配合。
func Escalate(ctx context.Context, lvl slog.Level) {
	f := from(ctx)
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if lvl > f.level {
		f.level = lvl
	}
}

// Collect 取字段袋的快照，交给中间件写汇总行。没有字段袋时返回 nil 与 Info。
func Collect(ctx context.Context) ([]any, slog.Level) {
	f := from(ctx)
	if f == nil {
		return nil, slog.LevelInfo
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]any, len(f.kv))
	copy(out, f.kv)
	return out, f.level
}
