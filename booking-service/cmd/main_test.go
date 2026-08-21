package main

import (
	"log/slog"
	"testing"
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
