package config

import (
	"os"
	"strconv"
	"time"
)

// Media defaults: WhatsApp's own limit for photos, audio and video is 16 MiB; attachments nobody
// fetched are removed after a day. Documents can be larger, up to MaxMediaBytes.
const (
	DefaultMediaMaxBytes = 16 << 20
	MaxMediaBytes        = 100 << 20
	DefaultMediaTTL      = 24 * time.Hour
)

type Config struct {
	Addr          string
	APIKey        string
	DataDir       string
	LogLevel      string
	WebhookSecret string
	WebhookURL    string
	// MediaMaxBytes is 0 when ZYLPHER_MEDIA_MAX_BYTES is invalid; main refuses to start.
	MediaMaxBytes int64
	// MediaTTL is 0 when ZYLPHER_MEDIA_TTL is invalid; main refuses to start.
	MediaTTL time.Duration
}

func Load() Config {
	return Config{
		Addr:          env("ZYLPHER_ADDR", "127.0.0.1:8080"),
		APIKey:        os.Getenv("ZYLPHER_API_KEY"),
		DataDir:       env("ZYLPHER_DATA_DIR", "data"),
		LogLevel:      env("ZYLPHER_LOG_LEVEL", "info"),
		WebhookSecret: os.Getenv("ZYLPHER_WEBHOOK_SECRET"),
		WebhookURL:    os.Getenv("ZYLPHER_WEBHOOK_URL"),
		MediaMaxBytes: mediaMaxBytes(os.Getenv("ZYLPHER_MEDIA_MAX_BYTES")),
		MediaTTL:      mediaTTL(os.Getenv("ZYLPHER_MEDIA_TTL")),
	}
}

func mediaMaxBytes(value string) int64 {
	if value == "" {
		return DefaultMediaMaxBytes
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 || parsed > MaxMediaBytes {
		return 0
	}
	return parsed
}

func mediaTTL(value string) time.Duration {
	if value == "" {
		return DefaultMediaTTL
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < time.Minute {
		return 0
	}
	return parsed
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
