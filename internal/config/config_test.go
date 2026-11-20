package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("ZYLPHER_ADDR", "")
	t.Setenv("ZYLPHER_API_KEY", "")
	t.Setenv("ZYLPHER_DATA_DIR", "")
	t.Setenv("ZYLPHER_LOG_LEVEL", "")
	t.Setenv("ZYLPHER_WEBHOOK_URL", "")
	t.Setenv("ZYLPHER_WEBHOOK_SECRET", "")
	t.Setenv("ZYLPHER_MEDIA_MAX_BYTES", "")
	t.Setenv("ZYLPHER_MEDIA_TTL", "")

	cfg := Load()

	if cfg.Addr != "127.0.0.1:8080" {
		t.Fatalf("Addr = %q, want %q", cfg.Addr, "127.0.0.1:8080")
	}
	if cfg.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", cfg.APIKey)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.DataDir != "data" {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, "data")
	}
	if cfg.WebhookURL != "" {
		t.Fatalf("WebhookURL = %q, want empty", cfg.WebhookURL)
	}
	if cfg.WebhookSecret != "" {
		t.Fatalf("WebhookSecret = %q, want empty", cfg.WebhookSecret)
	}
	if cfg.MediaMaxBytes != DefaultMediaMaxBytes || cfg.MediaTTL != DefaultMediaTTL {
		t.Fatalf("media = %d, %v", cfg.MediaMaxBytes, cfg.MediaTTL)
	}
}

func TestLoadMediaLimits(t *testing.T) {
	for _, c := range []struct {
		max, ttl string
		wantMax  int64
		wantTTL  time.Duration
	}{
		{"1048576", "2h", 1 << 20, 2 * time.Hour},
		{"0", "30s", 0, 0},
		{"abc", "dia", 0, 0},
		{"209715200", "-1h", 0, 0},
	} {
		t.Setenv("ZYLPHER_MEDIA_MAX_BYTES", c.max)
		t.Setenv("ZYLPHER_MEDIA_TTL", c.ttl)
		cfg := Load()
		if cfg.MediaMaxBytes != c.wantMax || cfg.MediaTTL != c.wantTTL {
			t.Fatalf("%q/%q = %d, %v", c.max, c.ttl, cfg.MediaMaxBytes, cfg.MediaTTL)
		}
	}
}

func TestLoadEnv(t *testing.T) {
	t.Setenv("ZYLPHER_ADDR", "127.0.0.1:9090")
	t.Setenv("ZYLPHER_API_KEY", "test-key")
	t.Setenv("ZYLPHER_DATA_DIR", "tmp-data")
	t.Setenv("ZYLPHER_LOG_LEVEL", "debug")
	t.Setenv("ZYLPHER_WEBHOOK_URL", "http://127.0.0.1/webhook")
	t.Setenv("ZYLPHER_WEBHOOK_SECRET", "webhook-secret")

	cfg := Load()

	if cfg.Addr != "127.0.0.1:9090" {
		t.Fatalf("Addr = %q, want %q", cfg.Addr, "127.0.0.1:9090")
	}
	if cfg.APIKey != "test-key" {
		t.Fatalf("APIKey = %q, want %q", cfg.APIKey, "test-key")
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.DataDir != "tmp-data" {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, "tmp-data")
	}
	if cfg.WebhookURL != "http://127.0.0.1/webhook" {
		t.Fatalf("WebhookURL = %q, want %q", cfg.WebhookURL, "http://127.0.0.1/webhook")
	}
	if cfg.WebhookSecret != "webhook-secret" {
		t.Fatalf("WebhookSecret = %q, want %q", cfg.WebhookSecret, "webhook-secret")
	}
}
