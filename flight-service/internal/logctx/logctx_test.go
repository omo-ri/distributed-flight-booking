package logctx_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/logctx"
)

func TestAddAndCollect(t *testing.T) {
	ctx := logctx.New(context.Background(), "trace-1")

	logctx.Add(ctx, logctx.KeyCache, logctx.CacheHit, logctx.KeyRows, 2)

	kv, lvl := logctx.Collect(ctx)
	if lvl != slog.LevelInfo {
		t.Errorf("基线级别应为 Info，得到 %v", lvl)
	}
	want := []any{logctx.KeyCache, logctx.CacheHit, logctx.KeyRows, 2}
	if len(kv) != len(want) {
		t.Fatalf("字段数不符：want %v, got %v", want, kv)
	}
	for i := range want {
		if kv[i] != want[i] {
			t.Errorf("kv[%d] = %v, want %v", i, kv[i], want[i])
		}
	}
	if got := logctx.TraceID(ctx); got != "trace-1" {
		t.Errorf("TraceID() = %q, want %q", got, "trace-1")
	}
}

// 同名 key 覆盖而不是追加——一条 JSON 日志里出现两个 degraded 没有意义。
func TestAddReplacesDuplicateKey(t *testing.T) {
	ctx := logctx.New(context.Background(), "trace-1")

	logctx.Add(ctx, logctx.KeyDegraded, "cache_set_failed")
	logctx.Add(ctx, logctx.KeyDegraded, "search_cache_invalidate_failed")

	kv, _ := logctx.Collect(ctx)
	if len(kv) != 2 {
		t.Fatalf("同名 key 应覆盖，得到 %v", kv)
	}
	if kv[1] != "search_cache_invalidate_failed" {
		t.Errorf("应保留最后一次写入，得到 %v", kv[1])
	}
}

func TestEscalateOnlyRaises(t *testing.T) {
	ctx := logctx.New(context.Background(), "trace-1")

	logctx.Escalate(ctx, slog.LevelWarn)
	if _, lvl := logctx.Collect(ctx); lvl != slog.LevelWarn {
		t.Fatalf("Warn 应抬高级别，得到 %v", lvl)
	}

	logctx.Escalate(ctx, slog.LevelDebug)
	if _, lvl := logctx.Collect(ctx); lvl != slog.LevelWarn {
		t.Errorf("Debug 不应降级，得到 %v", lvl)
	}
}

// 没有字段袋时（后台任务、单测直接调下层）静默丢弃，不 panic。
func TestNoBagIsNoOp(t *testing.T) {
	ctx := context.Background()

	logctx.Add(ctx, logctx.KeyCache, logctx.CacheMiss)
	logctx.Escalate(ctx, slog.LevelError)

	kv, lvl := logctx.Collect(ctx)
	if kv != nil {
		t.Errorf("无字段袋时应返回 nil，得到 %v", kv)
	}
	if lvl != slog.LevelInfo {
		t.Errorf("无字段袋时应返回 Info，得到 %v", lvl)
	}
	if got := logctx.TraceID(ctx); got != "" {
		t.Errorf("无字段袋时 TraceID 应为空串，得到 %q", got)
	}
}

// 字段袋跨 goroutine 写：cache 与 service 可能不在同一个 goroutine 上。
func TestConcurrentAddIsRaceFree(t *testing.T) {
	ctx := logctx.New(context.Background(), "trace-1")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			logctx.Add(ctx, logctx.KeyRows, i)
			logctx.Escalate(ctx, slog.LevelWarn)
			logctx.Collect(ctx)
		}(i)
	}
	wg.Wait()

	kv, lvl := logctx.Collect(ctx)
	if len(kv) != 2 {
		t.Errorf("同名 key 并发写仍应只有一份，得到 %v", kv)
	}
	if lvl != slog.LevelWarn {
		t.Errorf("级别应为 Warn，得到 %v", lvl)
	}
}
