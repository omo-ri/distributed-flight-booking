package auth

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/logctx"
)

// UnaryInterceptor returns a gRPC unary interceptor that validates the API key
// from the "x-api-key" metadata field.
func UnaryInterceptor(apiKey string) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			logctx.Add(ctx, logctx.KeyReason, "missing_metadata")
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		keys := md.Get("x-api-key")
		if len(keys) == 0 || keys[0] != apiKey {
			logctx.Add(ctx, logctx.KeyReason, "invalid_api_key")
			return nil, status.Error(codes.Unauthenticated, "invalid api key")
		}

		// 认证通过不打日志——这件事汇总行的 code=OK 已经说了，
		// 单独打一行等于每个请求多一行，量等于总请求数。
		return handler(ctx, req)
	}
}
