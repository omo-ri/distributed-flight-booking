package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/auth"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/cache"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/handler"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/logging"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/metrics"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/repository"
	"github.com/omo-ri/distributed-flight-booking/flight-service/internal/service"
	pb "github.com/omo-ri/distributed-flight-booking/flight-service/pb/flight"
)

func main() {
	// 与 booking-service 同构的结构化日志：slog + JSON + LOG_LEVEL。
	// 没有级别开关的请求路径日志会在压测里把磁盘写满（实测 770 万行），
	// 所以任何请求级日志都必须先有这个开关（CLAUDE.md § 4）。
	logLevel, levelErr := parseLogLevel(envOrDefault("LOG_LEVEL", "info"))
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(log)
	if levelErr != nil {
		log.Warn("invalid LOG_LEVEL, falling back to info", "value", os.Getenv("LOG_LEVEL"), "error", levelErr)
	}

	// go-redis 默认往标准库 log 写（sentinel 选主、故障转移都走这里），
	// 不接管的话 flight-service 的输出里会混进非 JSON 行，D-11 第 1 点
	// 「一套解析规则覆盖两个服务」就不成立。
	redis.SetLogger(redisLogger{log: log})

	ctx := context.Background()

	// 配置读取集中在启动阶段，读完打一条 config loaded。密码不进日志。
	pgCfg := repository.PostgresConfig{
		Host:     envOrDefault("DB_HOST", "localhost"),
		Port:     envOrDefault("DB_PORT", "5432"),
		User:     envOrDefault("DB_USER", "flight"),
		Password: envOrDefault("DB_PASSWORD", "flight_pass"),
		DBName:   envOrDefault("DB_NAME", "flight_db"),
	}
	sentinelAddr := os.Getenv("REDIS_SENTINEL_ADDR")
	masterName := envOrDefault("REDIS_MASTER_NAME", "mymaster")
	redisAddr := os.Getenv("REDIS_ADDR")
	apiKey := envOrDefault("AUTH_API_KEY", "")
	port := envOrDefault("GRPC_PORT", "50051")
	metricsPort := envOrDefault("METRICS_PORT", "9091")

	log.Info("config loaded",
		"log_level", logLevel.String(),
		"grpc_port", port,
		"metrics_port", metricsPort,
		"db_host", pgCfg.Host,
		"db_port", pgCfg.Port,
		"db_user", pgCfg.User,
		"db_name", pgCfg.DBName,
		"redis_sentinel_addr", sentinelAddr,
		"redis_master_name", masterName,
		"redis_addr", redisAddr,
		"auth_api_key_set", apiKey != "",
	)

	// 启动分步日志只标成败，不带配置值。
	log.Info("running database migrations")
	if err := runMigrations(pgCfg); err != nil {
		log.Error("migration failed", "error", err)
		os.Exit(1)
	}
	log.Info("migrations completed")

	db, err := repository.NewDB(ctx, pgCfg)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	log.Info("database connected")

	// Redis cache (Sentinel mode or direct mode)
	var redisCache *cache.RedisCache
	switch {
	case sentinelAddr != "":
		redisCache, err = cache.NewRedisSentinelCache(sentinelAddr, masterName)
		if err != nil {
			log.Error("failed to connect to redis sentinel", "error", err)
			os.Exit(1)
		}
		defer redisCache.Close()
		log.Info("redis connected", "mode", "sentinel")
	case redisAddr != "":
		redisCache, err = cache.NewRedisCache(redisAddr)
		if err != nil {
			log.Error("failed to connect to redis", "error", err)
			os.Exit(1)
		}
		defer redisCache.Close()
		log.Info("redis connected", "mode", "direct")
	default:
		log.Warn("running without cache", "reason", "neither REDIS_SENTINEL_ADDR nor REDIS_ADDR is set")
	}

	// Layers
	repo := repository.NewFlightRepo(db)
	svc := service.NewFlightService(repo, redisCache)
	h := handler.NewFlightHandler(svc)

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Error("failed to listen", "error", err)
		os.Exit(1)
	}

	// 拦截器顺序：logging 必须最外层，否则被 auth 挡掉的请求在日志里不存在。
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			logging.UnaryServerInterceptor(log),
			metrics.UnaryServerInterceptor(),
			auth.UnaryInterceptor(apiKey),
		),
	)
	pb.RegisterFlightServiceServer(srv, h)

	// Metrics HTTP server (separate port — gRPC and HTTP can't share a listener here).
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		log.Info("metrics server listening")
		if err := http.ListenAndServe(":"+metricsPort, mux); err != nil {
			log.Error("metrics server stopped", "error", err)
		}
	}()

	log.Info("flight-service listening")
	if err := srv.Serve(lis); err != nil {
		log.Error("grpc server stopped", "error", err)
		os.Exit(1)
	}
}

// redisLogger 把 go-redis 的输出接进 slog。级别定在 Warn：go-redis 只在
// 值得注意的事件上说话（连接异常、sentinel 选主、故障转移），而故障转移
// 恰恰是压测时切 LOG_LEVEL=warn 之后仍然必须看得见的东西。
type redisLogger struct{ log *slog.Logger }

func (l redisLogger) Printf(ctx context.Context, format string, v ...any) {
	l.log.WarnContext(ctx, fmt.Sprintf(format, v...), "source", "go-redis")
}

// parseLogLevel 把 LOG_LEVEL 解析成 slog.Level，与 booking-service 同构：
// 接受 debug / info / warn / error（大小写不敏感），也接受 slog 的偏移写法如
// "info+2"。解析不了就退回 info 并把原值报出来——取值来自部署环境（compose /
// k8s），拼错不该让服务起不来，也不该悄悄变成 debug 把压测淹了。
func parseLogLevel(s string) (slog.Level, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo, err
	}
	return lvl, nil
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
