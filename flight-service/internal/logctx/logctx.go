// Package logctx 承载「一次 RPC 一条汇总行」这个口径所需的字段袋。
//
// 与 booking-service 的同名包同构但各写一份：两个服务是独立 Go module，
// 除 pb 契约层外不共享代码（CLAUDE.md § 4）。字段白名单不同——flight 侧
// 挂的是 cache / rows，booking 侧挂的是 booking_id / cb / retries 等。
//
// 口径（CLAUDE.md § 4）：正常请求路径上 handler / service / cache 一行日志都不打，
// 值得记的东西挂进 context 里的字段袋，由 logging 拦截器在收尾时写进那一条汇总行。
package logctx

import (
	"context"
	"log/slog"
	"sync"
)

// 固有字段：由拦截器无条件写入，不走字段袋。
const KeyTraceID = "trace_id"

// 业务字段：flight 侧的白名单，全表见 docs/conventions/engineering.md。
const (
	KeyCache = "cache" // hit / miss / bypass
	KeyRows  = "rows"  // 返回条数
)

// 结果字段：描述这一条 RPC 怎么结束的。
const (
	KeyOutcome = "outcome"
	KeyReason  = "reason"
	KeyError   = "error"
)

// 韧性字段：只在异常路径上出现。
const KeyDegraded = "degraded"

const OutcomeRejected = "rejected"

// 缓存读结果的取值，避免各处写字面量拼错。
const (
	CacheHit    = "hit"
	CacheMiss   = "miss"
	CacheBypass = "bypass" // 没配 Redis，整条路径绕过缓存
)

type ctxKey struct{}

type fields struct {
	mu      sync.Mutex
	traceID string
	kv      []any
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

// TraceID 返回本次 RPC 的 trace_id；没有字段袋时返回空串。
func TraceID(ctx context.Context) string {
	f := from(ctx)
	if f == nil {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.traceID
}

// Add 挂载 key / value 对。同名 key 覆盖而不是追加；没有字段袋时静默丢弃。
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

// Escalate 抬高汇总行的级别，只升不降。用它表达「异常但已自动处理」。
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

// Collect 取字段袋的快照，交给拦截器写汇总行。
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
