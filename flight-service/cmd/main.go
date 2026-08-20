package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/auth"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/cache"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/handler"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/metrics"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/repository"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/service"
	pb "github.com/omo-ri/distributed-flight-booking/flight-service/pb/flight"
)

func main() {
	ctx := context.Background()

	pgCfg := repository.PostgresConfig{
		Host:     envOrDefault("DB_HOST", "localhost"),
		Port:     envOrDefault("DB_PORT", "5432"),
		User:     envOrDefault("DB_USER", "flight"),
		Password: envOrDefault("DB_PASSWORD", "flight_pass"),
		DBName:   envOrDefault("DB_NAME", "flight_db"),
	}

	// Run migrations
	if err := runMigrations(pgCfg); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	// Database
	db, err := repository.NewDB(ctx, pgCfg)
	if err != nil {
		log.Fatalf("connect to db: %v", err)
	}
	defer db.Close()

	// Redis cache (Sentinel mode or direct mode)
	var redisCache *cache.RedisCache
	if sentinelAddr := os.Getenv("REDIS_SENTINEL_ADDR"); sentinelAddr != "" {
		masterName := envOrDefault("REDIS_MASTER_NAME", "mymaster")
		redisCache, err = cache.NewRedisSentinelCache(sentinelAddr, masterName)
		if err != nil {
			log.Fatalf("connect to redis sentinel: %v", err)
		}
		defer redisCache.Close()
	} else if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		redisCache, err = cache.NewRedisCache(addr)
		if err != nil {
			log.Fatalf("connect to redis: %v", err)
		}
		defer redisCache.Close()
		log.Printf("redis connected: %s", addr)
	} else {
		log.Printf("REDIS_ADDR not set — running without cache")
	}

	// Layers
	repo := repository.NewFlightRepo(db)
	svc := service.NewFlightService(repo, redisCache)
	h := handler.NewFlightHandler(svc)

	// gRPC server with auth interceptor
	apiKey := envOrDefault("AUTH_API_KEY", "")
	port := envOrDefault("GRPC_PORT", "50051")
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			metrics.UnaryServerInterceptor(),
			auth.UnaryInterceptor(apiKey),
		),
	)
	pb.RegisterFlightServiceServer(srv, h)

	// Metrics HTTP server (separate port — gRPC and HTTP can't share a listener here).
	metricsPort := envOrDefault("METRICS_PORT", "9091")
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		log.Printf("flight-service metrics listening on :%s", metricsPort)
		if err := http.ListenAndServe(":"+metricsPort, mux); err != nil {
			log.Printf("metrics server stopped: %v", err)
		}
	}()

	log.Printf("flight-service listening on :%s", port)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func runMigrations(cfg repository.PostgresConfig) error {
	m, err := migrate.New("file://migrations", cfg.DSN())
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
