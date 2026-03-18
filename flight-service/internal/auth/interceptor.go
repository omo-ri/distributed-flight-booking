package auth

import (
	"context"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
			log.Printf("[AUTH] REJECTED %s — no metadata", info.FullMethod)
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		keys := md.Get("x-api-key")
		if len(keys) == 0 || keys[0] != apiKey {
			log.Printf("[AUTH] REJECTED %s — invalid api key", info.FullMethod)
			return nil, status.Error(codes.Unauthenticated, "invalid api key")
		}

		log.Printf("[AUTH] OK %s", info.FullMethod)
		return handler(ctx, req)
	}
}