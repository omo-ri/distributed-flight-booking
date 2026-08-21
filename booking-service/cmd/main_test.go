package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/logctx"
)

// LOG_LEVEL 的取值来自部署环境（compose / k8s），拼错不该让服务起不来，
// 也不该悄悄变成 debug 把压测淹了——退回 info 是唯一安全的默认。
func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"debug", slog.LevelDebug, false},
		{"info", slog.LevelInfo, false},
		{"warn", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"WARN", slog.LevelWarn, false},       // 大小写不敏感
		{"info+2", slog.LevelInfo + 2, false}, // slog 的偏移写法
		{"", slog.LevelInfo, true},
		{"verbose", slog.LevelInfo, true}, // 拼错 → 退回 info 并报错
	}

	for _, tt := range tests {
		got, err := parseLogLevel(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseLogLevel(%q) err = %v, wantErr = %v", tt.in, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("parseLogLevel(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// 汇总行的级别是一张判断表，而判断表就是会写错——改造前 handler 把「座位不足」
// 打成 Warn 就是活证据。这组测试把那张表钉住：级别按 outcome 判，不按
// 「这事看起来有多严重」判（CLAUDE.md § 4）。
func TestRequestLoggerLevelAndFields(t *testing.T) {
	tests := []struct {
		name      string
		handler   echo.HandlerFunc
		wantLevel string
		wantAttrs map[string]any
		absent    []string
	}{
		{
			name:      "成功请求是 Info",
			handler:   func(c echo.Context) error { return c.NoContent(http.StatusOK) },
			wantLevel: "INFO",
			wantAttrs: map[string]any{"status": float64(200), "route": "/bookings/:id"},
			// 熔断闭合是常态，cb 字段不该出现
			absent: []string{logctx.KeyCB, logctx.KeyOutcome, logctx.KeyError, logctx.KeyDegraded},
		},
		{
			name: "业务拒绝是 Info 不是 Warn",
			handler: func(c echo.Context) error {
				logctx.Add(c.Request().Context(),
					logctx.KeyOutcome, logctx.OutcomeRejected,
					logctx.KeyReason, "insufficient_seats")
				return c.NoContent(http.StatusConflict)
			},
			wantLevel: "INFO",
			wantAttrs: map[string]any{
				"status":          float64(409),
				logctx.KeyOutcome: logctx.OutcomeRejected,
				logctx.KeyReason:  "insufficient_seats",
			},
			absent: []string{logctx.KeyError},
		},
		{
			name: "已自动处理的降级是 Warn",
			handler: func(c echo.Context) error {
				ctx := c.Request().Context()
				logctx.Escalate(ctx, slog.LevelWarn)
				logctx.Add(ctx, logctx.KeyDegraded, "retry_recovered", logctx.KeyRetries, 2)
				return c.NoContent(http.StatusOK)
			},
			wantLevel: "WARN",
			wantAttrs: map[string]any{
				"status":           float64(200),
				logctx.KeyDegraded: "retry_recovered",
				logctx.KeyRetries:  float64(2),
			},
		},
		{
			name: "内部错误是 Error，带 wrap 链",
			handler: func(c echo.Context) error {
				logctx.Add(c.Request().Context(), logctx.KeyError, "reserve seats: rpc error: Unavailable")
				return c.NoContent(http.StatusInternalServerError)
			},
			wantLevel: "ERROR",
			wantAttrs: map[string]any{
				"status":        float64(500),
				logctx.KeyError: "reserve seats: rpc error: Unavailable",
			},
		},
		{
			name: "熔断拒绝的 503 是 Error，cb 字段可分辨原因",
			handler: func(c echo.Context) error {
				logctx.Add(c.Request().Context(), logctx.KeyCB, "open", logctx.KeyReason, "circuit_open")
				return c.NoContent(http.StatusServiceUnavailable)
			},
			wantLevel: "ERROR",
			wantAttrs: map[string]any{
				"status":         float64(503),
				logctx.KeyCB:     "open",
				logctx.KeyReason: "circuit_open",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := runRequest(t, tt.handler, nil)

			if got := line["level"]; got != tt.wantLevel {
				t.Errorf("level = %v, want %v", got, tt.wantLevel)
			}
			if got := line["msg"]; got != "request" {
				t.Errorf("msg = %v, want request", got)
			}
			for k, want := range tt.wantAttrs {
				if got := line[k]; got != want {
					t.Errorf("字段 %s = %#v, want %#v", k, got, want)
				}
			}
			for _, k := range tt.absent {
				if _, ok := line[k]; ok {
					t.Errorf("字段 %s 不该出现在这一行里：%v", k, line[k])
				}
			}
		})
	}
}

// 固有字段：每条汇总行无条件都有，缺一个都会让这行读不成一次完整的请求。
func TestRequestLoggerAlwaysHasFixedFields(t *testing.T) {
	line := runRequest(t, func(c echo.Context) error { return c.NoContent(http.StatusOK) }, nil)

	for _, k := range []string{logctx.KeyTraceID, "route", "method", "path", "status", "latency_ms"} {
		if _, ok := line[k]; !ok {
			t.Errorf("固有字段 %s 缺失：%v", k, line)
		}
	}
	if line["path"] != "/bookings/abc" {
		t.Errorf("path 应是实例路径，得到 %v", line["path"])
	}
	if line["route"] != "/bookings/:id" {
		t.Errorf("route 应是路由模板，得到 %v", line["route"])
	}
}

// trace_id 优先采用调用方传入的 X-Request-ID——跨服务串联的起点可能在本服务之外。
func TestRequestLoggerAdoptsIncomingTraceID(t *testing.T) {
	line := runRequest(t, func(c echo.Context) error { return c.NoContent(http.StatusOK) },
		map[string]string{echo.HeaderXRequestID: "trace-from-caller"})

	if line[logctx.KeyTraceID] != "trace-from-caller" {
		t.Errorf("trace_id = %v, want trace-from-caller", line[logctx.KeyTraceID])
	}
}

// 一次请求只出一条汇总行——这是整套口径的地基，改造前同一个请求出 9 行。
func TestRequestLoggerEmitsExactlyOneLine(t *testing.T) {
	var buf bytes.Buffer
	e := newTestEcho(&buf)
	e.GET("/bookings/:id", func(c echo.Context) error { return c.NoContent(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/bookings/abc", nil)
	e.ServeHTTP(httptest.NewRecorder(), req)

	if got := strings.Count(strings.TrimSpace(buf.String()), "\n"); got != 0 {
		t.Errorf("一次请求应只出一条日志，得到 %d 条：%s", got+1, buf.String())
	}
}

// Prometheus 每 5 秒抓一次 /metrics，给它打汇总行只会得到一条恒定速率的噪音。
// 它挂了由 Prometheus 的 up 指标回答，那本来就是 up 存在的意义。
func TestRequestLoggerSkipsMetricsScrape(t *testing.T) {
	var buf bytes.Buffer
	e := newTestEcho(&buf)
	e.GET(metricsPath, func(c echo.Context) error { return c.NoContent(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, metricsPath, nil)
	e.ServeHTTP(httptest.NewRecorder(), req)

	if buf.Len() != 0 {
		t.Errorf("/metrics 抓取不该打日志，得到：%s", buf.String())
	}
}

func newTestEcho(buf *bytes.Buffer) *echo.Echo {
	log := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.RequestID())
	e.Use(requestLogger(log))
	return e
}

func runRequest(t *testing.T, h echo.HandlerFunc, headers map[string]string) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	e := newTestEcho(&buf)
	e.GET("/bookings/:id", h)

	req := httptest.NewRequest(http.MethodGet, "/bookings/abc", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	e.ServeHTTP(httptest.NewRecorder(), req)

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("汇总行不是合法 JSON：%v（原文：%s）", err, buf.String())
	}
	return line
}
