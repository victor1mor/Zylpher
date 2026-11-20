package logging

import (
	"context"
	"log/slog"
	"testing"
)

func TestNewSetsLevel(t *testing.T) {
	tests := []struct {
		name         string
		level        string
		debugEnabled bool
		infoEnabled  bool
	}{
		{name: "debug", level: "debug", debugEnabled: true, infoEnabled: true},
		{name: "info default", level: "info", debugEnabled: false, infoEnabled: true},
		{name: "unknown defaults to info", level: "verbose", debugEnabled: false, infoEnabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := New(tt.level)

			if got := logger.Enabled(context.Background(), slog.LevelDebug); got != tt.debugEnabled {
				t.Fatalf("debug enabled = %v, want %v", got, tt.debugEnabled)
			}
			if got := logger.Enabled(context.Background(), slog.LevelInfo); got != tt.infoEnabled {
				t.Fatalf("info enabled = %v, want %v", got, tt.infoEnabled)
			}
		})
	}
}
