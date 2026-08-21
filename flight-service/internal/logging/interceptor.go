// Package logging 提供 flight-service 的请求汇总行拦截器。
//
// 为什么它不和 metrics 拦截器合并：测量与记录的生命周期不同。合了之后
// 想只给日志加字段、只给日志做采样，都得动指标代码，两者也没法各测各的。
// 省下来的只是一次 time.Since——不值这个耦合。
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/logctx"
)

// TraceIDMetadataKey 是 trace_id 跨 gRPC 的载体。booking-service 在
// grpcclient 里往 outgoing metadata 挂同名键，两侧的汇总行靠它串起来。
const TraceIDMetadataKey = "x-trace-id"

// UnaryServerInterceptor 为每次 RPC 打一条汇总行。
//
// 它必须是拦截器链的最外层：auth 拒绝、metrics 计数都要落在这条线内，
// 否则被 auth 挡掉的请求在日志里根本不存在。
func UnaryServerInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		ctx = logctx.New(ctx, traceIDFrom(ctx))

		resp, err := handler(ctx, req)

		code := status.Code(err)
		attrs, level := logctx.Collect(ctx)
		if lvl := levelForCode(code); lvl > level {
			level = lvl
		}
		if err != nil {
			if isRejection(code) {
				logctx.Add(ctx, logctx.KeyOutcome, logctx.OutcomeRejected)
			} else {
				logctx.Add(ctx, logctx.KeyError, err.Error())
			}
			attrs, _ = logctx.Collect(ctx)
		}

		args := make([]any, 0, 8+len(attrs))
		args = append(args,
			logctx.KeyTraceID, logctx.TraceID(ctx),
			"route", info.FullMethod,
			"code", code.String(),
			"latency_ms", time.Since(start).Milliseconds(),
		)
		args = append(args, attrs...)

		log.Log(ctx, level, "rpc", args...)
		return resp, err
	}
}

// levelForCode 把 gRPC 状态码映射成日志级别。判据是"谁需要看"（CLAUDE.md § 4）：
// 业务拒绝没人需要为它做什么，所以是 Info——把座位不足打成 Warn 会让
// 「有多少事真的需要人看」这个信号失真，和压测错误率把 409 算进失败是同一个错误。
func levelForCode(code codes.Code) slog.Level {
	switch code {
	case codes.OK:
		return slog.LevelInfo
	case codes.NotFound, codes.InvalidArgument, codes.AlreadyExists,
		codes.FailedPrecondition, codes.ResourceExhausted, codes.OutOfRange,
		codes.Canceled:
		return slog.LevelInfo
	case codes.Unauthenticated, codes.PermissionDenied, codes.Unimplemented:
		// 单条认证失败是"异常但已自动处理"——拒绝本身就是处理。
		// 真需要人介入的是"大量 401"，那是告警该回答的问题，不是单条日志。
		return slog.LevelWarn
	default:
		return slog.LevelError
	}
}

func isRejection(code codes.Code) bool {
	return levelForCode(code) == slog.LevelInfo && code != codes.OK
}

// traceIDFrom 取调用方传来的 trace_id；没有就自己生成一个当根。
// 生成用 crypto/rand 而不是引 uuid 依赖——这里要的只是一个够随机的不透明串。
func traceIDFrom(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(TraceIDMetadataKey); len(vals) > 0 && vals[0] != "" {
			return vals[0]
		}
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
