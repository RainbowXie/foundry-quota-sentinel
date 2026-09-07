package main

import (
	"os"
	"testing"
)

func TestOcgtPort(t *testing.T) {
	// 保存并还原环境现场，避免影响其他测试用例
	orig := os.Getenv("FQS_PORT")
	defer func() {
		if orig != "" {
			_ = os.Setenv("FQS_PORT", orig)
		} else {
			_ = os.Unsetenv("FQS_PORT")
		}
	}()

	tests := []struct {
		name     string
		envVal   string
		setEnv   bool
		wantPort int
	}{
		{
			name:     "unset env returns default 8788",
			setEnv:   false,
			wantPort: 8788,
		},
		{
			name:     "empty env returns default 8788",
			envVal:   "",
			setEnv:   true,
			wantPort: 8788,
		},
		{
			name:     "valid custom port 9000",
			envVal:   "9000",
			setEnv:   true,
			wantPort: 9000,
		},
		{
			name:     "boundary min port 1",
			envVal:   "1",
			setEnv:   true,
			wantPort: 1,
		},
		{
			name:     "boundary max port 65535",
			envVal:   "65535",
			setEnv:   true,
			wantPort: 65535,
		},
		{
			name:     "boundary below min (0) falls back to default",
			envVal:   "0",
			setEnv:   true,
			wantPort: 8788,
		},
		{
			name:     "negative port falls back to default",
			envVal:   "-80",
			setEnv:   true,
			wantPort: 8788,
		},
		{
			name:     "boundary above max (65536) falls back to default",
			envVal:   "65536",
			setEnv:   true,
			wantPort: 8788,
		},
		{
			name:     "non-numeric port falls back to default",
			envVal:   "invalid_port",
			setEnv:   true,
			wantPort: 8788,
		},
		{
			name:     "space-padded port string falls back to default",
			envVal:   " 8788 ",
			setEnv:   true,
			wantPort: 8788,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				_ = os.Setenv("FQS_PORT", tt.envVal)
			} else {
				_ = os.Unsetenv("FQS_PORT")
			}

			got := ocgtPort()
			if got != tt.wantPort {
				t.Fatalf("ocgtPort() = %d, want %d (env=%q)", got, tt.wantPort, tt.envVal)
			}
		})
	}
}
