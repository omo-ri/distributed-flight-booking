package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/logctx"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/logging"
)

// 级别映射是一张判断表，判断表就是会写错。这组测试把它钉住：
// 业务拒绝是 Info，不是 Warn（CLAUDE.md § 4 的「谁需要看」判据）。
func TestInterceptorLevelAndFields(t *testing.T) {
	tests := []struct {
		name      string
		handler   grpc.UnaryHandler
		wantLevel string
		wantAttrs map[string]any
		absent    []string
	}{
		{
			name:      "成功是 Info",
			handler:   func(ctx context.Context, req any) (any, error) { return "ok", nil },
			wantLevel: "INFO",
			wantAttrs: map[string]any{"code": "OK"},
			absent:    []string{logctx.KeyError, logctx.KeyOutcome, logctx.KeyDegraded, logctx.KeyCache},
		},
		{
			name: "缓存命中挂在同一条汇总行上",
			handler: func(ctx context.Context, req any) (any, error) {
				logctx.Add(ctx, logctx.KeyCache, logctx.CacheHit, logctx.KeyRows, 3)
				return "ok", nil
			},
			wantLevel: "INFO",
			wantAttrs: map[string]any{
				"code":          "OK",
				logctx.KeyCache: logctx.CacheHit,
				logctx.KeyRows:  float64(3),
			},
		},
		{
			name: "业务拒绝是 Info 不是 Warn",
			handler: func(ctx context.Context, req any) (any, error) {
				return nil, status.Error(codes.NotFound, "flight not found")
			},
			wantLevel: "INFO",
			wantAttrs: map[string]any{
				"code":            "NotFound",
				logctx.KeyOutcome: logctx.OutcomeRejected,
			},
			// 业务拒绝不是错误，不该带 error 字段——否则「有多少事真的出错了」失真
			absent: []string{logctx.KeyError},
		},
		{
			name: "座位不足也是业务拒绝",
			handler: func(ctx context.Context, req any) (any, error) {
				return nil, status.Error(codes.ResourceExhausted, "not enough seats")
			},
			wantLevel: "INFO",
			wantAttrs: map[string]any{"code": "ResourceExhausted", logctx.KeyOutcome: logctx.OutcomeRejected},
		},
		{
			name: "认证拒绝是 Warn",
			handler: func(ctx context.Context, req any) (any, error) {
				logctx.Add(ctx, logctx.KeyReason, "invalid_api_key")
				return nil, status.Error(codes.Unauthenticated, "invalid api key")
			},
			wantLevel: "WARN",
			wantAttrs: map[string]any{"code": "Unauthenticated", logctx.KeyReason: "invalid_api_key"},
		},
		{
			name: "内部错误是 Error，带原因",
			handler: func(ctx context.Context, req any) (any, error) {
				return nil, status.Error(codes.Internal, "get flight: connection refused")
			},
			wantLevel: "ERROR",
			wantAttrs: map[string]any{"code": "Internal"},
		},
		{
			name: "已自动处理的降级是 Warn，即使 RPC 成功",
			handler: func(ctx context.Context, req any) (any, error) {
				logctx.Escalate(ctx, slog.LevelWarn)
				logctx.Add(ctx, logctx.KeyDegraded, "cache_set_failed")
				return "ok", nil
			},
			wantLevel: "WARN",
			wantAttrs: map[string]any{"code": "OK", logctx.KeyDegraded: "cache_set_failed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line, _ := invoke(t, tt.handler, nil)

			if got := line["level"]; got != tt.wantLevel {
				t.Errorf("level = %v, want %v", got, tt.wantLevel)
			}
			if got := line["msg"]; got != "rpc" {
				t.Errorf("msg = %v, want rpc", got)
			}
			for k, want := range tt.wantAttrs {
				if got := line[k]; got != want {
					t.Errorf("字段 %s = %#v, want %#v", k, got, want)
				}
			}
			for _, k := range tt.absent {
				if _, ok := line[k]; ok {
					t.Errorf("字段 %s 不该出现：%v", k, line[k])
				}
			}
		})
	}
}

// 内部错误必须带 error 字段——出故障时那一行要能直接读出发生了什么。
func TestInterceptorRecordsInternalError(t *testing.T) {
	line, _ := invoke(t, func(ctx context.Context, req any) (any, error) {
		return nil, status.Error(codes.Internal, "get flight: connection refused")
	}, nil)

	got, ok := line[logctx.KeyError].(string)
	if !ok || !strings.Contains(got, "connection refused") {
		t.Errorf("error 字段应带原始错误，得到 %#v", line[logctx.KeyError])
	}
}

// 固有字段：每条汇总行无条件都有。
func TestInterceptorAlwaysHasFixedFields(t *testing.T) {
	line, _ := invoke(t, func(ctx context.Context, req any) (any, error) { return "ok", nil }, nil)

	for _, k := range []string{logctx.KeyTraceID, "route", "code", "latency_ms"} {
		if _, ok := line[k]; !ok {
			t.Errorf("固有字段 %s 缺失：%v", k, line)
		}
	}
	if line["route"] != "/flight.FlightService/GetFlight" {
		t.Errorf("route 应是 gRPC 全方法名，得到 %v", line["route"])
	}
}

// trace_id 从 incoming metadata 取——这是跨服务串联的接收端。
// 发送端（booking 的 grpcclient 写 outgoing metadata）在 booking 侧各自测。
func TestInterceptorAdoptsIncomingTraceID(t *testing.T) {
	md := metadata.Pairs(logging.TraceIDMetadataKey, "trace-from-booking")
	line, ctx := invoke(t, func(ctx context.Context, req any) (any, error) { return "ok", nil }, md)

	if line[logctx.KeyTraceID] != "trace-from-booking" {
		t.Errorf("trace_id = %v, want trace-from-booking", line[logctx.KeyTraceID])
	}
	// 下层（cache / service）也要能读到同一个值
	if got := logctx.TraceID(ctx); got != "trace-from-booking" {
		t.Errorf("下层读到的 trace_id = %q, want trace-from-booking", got)
	}
}

// 没有上游 trace_id 时自己生成一个当根——flight-service 可能被别的调用方直接调。
func TestInterceptorGeneratesTraceIDWhenAbsent(t *testing.T) {
	line1, _ := invoke(t, func(ctx context.Context, req any) (any, error) { return "ok", nil }, nil)
	line2, _ := invoke(t, func(ctx context.Context, req any) (any, error) { return "ok", nil }, nil)

	id1, _ := line1[logctx.KeyTraceID].(string)
	id2, _ := line2[logctx.KeyTraceID].(string)
	if id1 == "" || id2 == "" {
		t.Fatalf("trace_id 不该为空：%q / %q", id1, id2)
	}
	if id1 == id2 {
		t.Errorf("两次生成的 trace_id 不该相同：%q", id1)
	}
}

// 一次 RPC 只出一条汇总行——改造前 auth + cache 每次至少两行。
func TestInterceptorEmitsExactlyOneLine(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	_, _ = logging.UnaryServerInterceptor(log)(
		context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/flight.FlightService/GetFlight"},
		func(ctx context.Context, req any) (any, error) {
			logctx.Add(ctx, logctx.KeyCache, logctx.CacheHit)
			return "ok", nil
		},
	)

	if got := strings.Count(strings.TrimSpace(buf.String()), "\n"); got != 0 {
		t.Errorf("一次 RPC 应只出一条日志，得到 %d 条：%s", got+1, buf.String())
	}
}

// invoke 跑一次拦截器，返回解析后的汇总行与 handler 见到的 ctx。
func invoke(t *testing.T, h grpc.UnaryHandler, md metadata.MD) (map[string]any, context.Context) {
	t.Helper()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx := context.Background()
	if md != nil {
		ctx = metadata.NewIncomingContext(ctx, md)
	}

	var seen context.Context
	_, _ = logging.UnaryServerInterceptor(log)(ctx, nil,
		&grpc.UnaryServerInfo{FullMethod: "/flight.FlightService/GetFlight"},
		func(ctx context.Context, req any) (any, error) {
			seen = ctx
			return h(ctx, req)
		},
	)

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("汇总行不是合法 JSON：%v（原文：%s）", err, buf.String())
	}
	return line, seen
}
