package main

import (
	"context"
	"errors"
	"log/slog"
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
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/metrics"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/repository"
	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/service"
)

func main() {
	// Structured JSON logger
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	ctx := context.Background()

	pgCfg := repository.PostgresConfig{
		Host:     envOrDefault("DB_HOST", "localhost"),
		Port:     envOrDefault("DB_PORT", "5432"),
		User:     envOrDefault("DB_USER", "booking"),
		Password: envOrDefault("DB_PASSWORD", "booking_pass"),
		DBName:   envOrDefault("DB_NAME", "booking_db"),
	}

	// Run migrations
	log.Info("running database migrations")
	if err := runMigrations(pgCfg); err != nil {
		log.Error("migration failed", "error", err)
		os.Exit(1)
	}
	log.Info("migrations completed")

	// Database
	db, err := repository.NewDB(ctx, pgCfg)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	log.Info("database connected", "host", pgCfg.Host, "db", pgCfg.DBName)

	// Circuit breaker (题10)
	cbThreshold := envOrDefaultInt("CB_ERROR_THRESHOLD", 5)
	cbTimeout := time.Duration(envOrDefaultInt("CB_TIMEOUT_SECONDS", 30)) * time.Second
	cbWindow := time.Duration(envOrDefaultInt("CB_WINDOW_SECONDS", 60)) * time.Second
	cb := circuitbreaker.New(cbThreshold, cbTimeout, cbWindow)
	log.Info("circuit breaker initialized",
		"threshold", cbThreshold,
		"timeout", cbTimeout,
		"window", cbWindow,
	)

	// gRPC client to Flight Service
	flightAddr := envOrDefault("FLIGHT_SERVICE_ADDR", "localhost:50051")
	apiKey := envOrDefault("AUTH_API_KEY", "")
	flightClient, err := grpcclient.NewFlightClient(flightAddr, apiKey, cb)
	if err != nil {
		log.Error("failed to connect to flight service", "addr", flightAddr, "error", err)
		os.Exit(1)
	}
	defer flightClient.Close()
	log.Info("flight service connected", "addr", flightAddr)

	// Layers
	repo := repository.NewBookingRepo(db)
	svc := service.NewBookingService(repo, flightClient, log)
	h := handler.NewBookingHandler(svc, log)

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
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))

	// Register OpenAPI routes
	api.RegisterHandlers(e, h)

	port := envOrDefault("HTTP_PORT", "8080")
	log.Info("booking-service starting", "port", port)
	if err := e.Start(":" + port); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// requestLogger returns Echo middleware that logs every request with slog.
func requestLogger(log *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			if err != nil {
				c.Error(err)
			}

			req := c.Request()
			res := c.Response()
			latency := time.Since(start)

			log.Info("request",
				"method", req.Method,
				"path", req.URL.Path,
				"query", req.URL.RawQuery,
				"status", res.Status,
				"latency_ms", latency.Milliseconds(),
				"request_id", c.Response().Header().Get(echo.HeaderXRequestID),
			)
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
