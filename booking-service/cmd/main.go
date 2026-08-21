package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/omo-ri/distributed-flight-booking/booking-service/api"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/circuitbreaker"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/grpcclient"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/handler"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/logctx"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/metrics"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/repository"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/service"
)

func main() {
	// Structured JSON logger
	logLevel, levelErr := parseLogLevel(envOrDefault("LOG_LEVEL", "info"))
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(log)
	if levelErr != nil {
		log.Warn("invalid LOG_LEVEL, falling back to info", "value", os.Getenv("LOG_LEVEL"), "error", levelErr)
	}

	ctx := context.Background()

	// 配置读取集中在启动阶段（CLAUDE.md § 4），读完打一条 config loaded：
	// 出故障时「这个容器到底是用什么配置起来的」一行可查，不用去翻 compose。
	// 密码不进日志，API key 只报「配没配」。
	pgCfg := repository.PostgresConfig{
		Host:     envOrDefault("DB_HOST", "localhost"),
		Port:     envOrDefault("DB_PORT", "5434"),
		User:     envOrDefault("DB_USER", "booking"),
		Password: envOrDefault("DB_PASSWORD", "booking_pass"),
		DBName:   envOrDefault("DB_NAME", "booking_db"),
	}
	cbThreshold := envOrDefaultInt("CB_ERROR_THRESHOLD", 5)
	cbTimeout := time.Duration(envOrDefaultInt("CB_TIMEOUT_SECONDS", 30)) * time.Second
	cbWindow := time.Duration(envOrDefaultInt("CB_WINDOW_SECONDS", 60)) * time.Second
	flightAddr := envOrDefault("FLIGHT_SERVICE_ADDR", "localhost:50051")
	apiKey := envOrDefault("AUTH_API_KEY", "")
	port := envOrDefault("HTTP_PORT", "8080")

	log.Info("config loaded",
		"log_level", logLevel.String(),
		"http_port", port,
		"db_host", pgCfg.Host,
		"db_port", pgCfg.Port,
		"db_user", pgCfg.User,
		"db_name", pgCfg.DBName,
		"flight_service_addr", flightAddr,
		"auth_api_key_set", apiKey != "",
		"cb_error_threshold", cbThreshold,
		"cb_timeout", cbTimeout,
		"cb_window", cbWindow,
	)

	// 启动分步日志只标成败，不带配置值——配置值上面那一条已经全了。
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

	// Circuit breaker (题10)
	cb := circuitbreaker.New(cbThreshold, cbTimeout, cbWindow)

	// gRPC client to Flight Service
	flightClient, err := grpcclient.NewFlightClient(flightAddr, apiKey, cb)
	if err != nil {
		log.Error("failed to connect to flight service", "error", err)
		os.Exit(1)
	}
	defer flightClient.Close()
	log.Info("flight service connected")

	// Layers
	repo := repository.NewBookingRepo(db)
	svc := service.NewBookingService(repo, flightClient)
	h := handler.NewBookingHandler(svc)

	// Echo server
	e := echo.New()
	e.HideBanner = true

	// Request ID middleware
	e.Use(middleware.RequestID())

	// Structured request logging middleware
	e.Use(requestLogger(log))

	// Recovery middleware
	e.Use(middleware.Recover())

	// Prometheus metrics middleware + endpoint
	e.Use(metrics.Middleware())
	e.GET(metricsPath, echo.WrapHandler(promhttp.Handler()))

	// Register OpenAPI routes
	api.RegisterHandlers(e, h)

	log.Info("booking-service listening")
	if err := e.Start(":" + port); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

const metricsPath = "/metrics"

// requestLogger 是「一次请求一条汇总行」这个口径的落点（CLAUDE.md § 4）。
// 它在请求入口装上 logctx 字段袋，下层把值得记的东西挂进去，收尾时一次性写出来——
// handler / service / grpcclient 因此没有 logger 可打，规则不靠人记。
//
// 级别按 outcome 判，不按「这事看起来有多严重」判：业务拒绝（4xx）是 Info，
// 因为没人需要为一次座位不足做什么；5xx 是 Error；下层登记过降级的是 Warn。
func requestLogger(log *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// /metrics 是 Prometheus 每 5 秒一次的抓取，属于观测面自己，不是业务
			// 请求路径。给它打汇总行只会得到一条恒定速率的噪音；它挂了由
			// Prometheus 的 up 指标回答，那本来就是 up 存在的意义。
			if c.Path() == metricsPath {
				return next(c)
			}

			start := time.Now()

			req := c.Request()
			// RequestID 中间件在本中间件之前跑，此时头里已经有值（外部传入的优先）。
			traceID := c.Response().Header().Get(echo.HeaderXRequestID)
			ctx := logctx.New(req.Context(), traceID)
			c.SetRequest(req.WithContext(ctx))

			err := next(c)
			if err != nil {
				c.Error(err)
				logctx.Add(ctx, logctx.KeyError, err.Error())
			}

			res := c.Response()
			attrs, level := logctx.Collect(ctx)
			if res.Status >= http.StatusInternalServerError {
				level = slog.LevelError
			}

			args := make([]any, 0, 12+len(attrs))
			args = append(args,
				logctx.KeyTraceID, traceID,
				"route", c.Path(), // 路由模板而非实例路径，用来聚合
				"method", req.Method,
				"path", req.URL.Path,
				"status", res.Status,
				"latency_ms", time.Since(start).Milliseconds(),
			)
			args = append(args, attrs...)

			log.Log(ctx, level, "request", args...)
			return nil
		}
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

// parseLogLevel 把 LOG_LEVEL 解析成 slog.Level。接受 debug / info / warn / error
// （大小写不敏感），也接受 slog 的偏移写法如 "info+2"。解析不了就退回 info 并
// 把原值报出来——压测时把它设成 warn，正常请求路径的日志就不再落盘。
func parseLogLevel(s string) (slog.Level, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo, err
	}
	return lvl, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
