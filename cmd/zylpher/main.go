package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/victor1mor/Zylpher/internal/api"
	"github.com/victor1mor/Zylpher/internal/config"
	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/engine"
	"github.com/victor1mor/Zylpher/internal/logging"
	"github.com/victor1mor/Zylpher/internal/media"
	"github.com/victor1mor/Zylpher/internal/session"
	"github.com/victor1mor/Zylpher/internal/storage"
	"github.com/victor1mor/Zylpher/internal/webhook"
)

func main() {
	cfg := config.Load()
	log := logging.New(cfg.LogLevel)
	if cfg.APIKey == "" {
		log.Error("ZYLPHER_API_KEY is required")
		os.Exit(1)
	}
	if cfg.WebhookURL != "" && cfg.WebhookSecret == "" {
		log.Error("ZYLPHER_WEBHOOK_SECRET is required when webhooks are enabled")
		os.Exit(1)
	}
	if cfg.MediaMaxBytes == 0 || cfg.MediaTTL == 0 {
		log.Error("ZYLPHER_MEDIA_MAX_BYTES must be 1 to 104857600 and ZYLPHER_MEDIA_TTL a duration of at least 1m")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var webhooks *webhook.Dispatcher
	if cfg.WebhookURL != "" {
		openCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var err error
		webhooks, err = webhook.OpenPersistentDispatcher(openCtx, filepath.Join(cfg.DataDir, "webhooks.db"), cfg.WebhookURL, cfg.WebhookSecret, nil)
		cancel()
		if err != nil {
			log.Error("failed to open webhook outbox", "error", err)
			os.Exit(1)
		}
		defer func() {
			if err := webhooks.Close(); err != nil {
				log.Error("failed to close webhook outbox", "error", err)
			}
		}()
	}
	store := storage.NewFileSessionStore(filepath.Join(cfg.DataDir, "sessions.json"))
	attachments := media.NewStore(filepath.Join(cfg.DataDir, "media"), cfg.MediaMaxBytes)
	messages := engine.NewWhatsAppPool(cfg.DataDir)
	defer func() {
		if err := messages.Close(); err != nil {
			log.Error("failed to close WhatsApp sessions", "error", err)
		}
	}()

	sessions, err := session.NewManagerWithStoreAndEngine(context.Background(), store, messages)
	if err != nil {
		log.Error("failed to load sessions", "error", err)
		os.Exit(1)
	}
	if webhooks != nil {
		deliver := func(event domain.WebhookEvent) {
			enqueueCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := webhooks.Dispatch(enqueueCtx, event); err != nil {
				log.Warn("failed to enqueue webhook", "event", event.Event)
			}
		}
		messages.SetMediaStore(attachments)
		messages.SetEventHandler(deliver)
		sessions.SetEventHandler(deliver)
		done := make(chan struct{})
		go func() {
			defer close(done)
			webhooks.Run(ctx, log)
		}()
		defer func() {
			stop()
			<-done
		}()
	}
	messages.SetStatusHandler(func(name string, status domain.SessionStatus) {
		if err := sessions.ApplyEngineStatus(context.Background(), name, status); err != nil {
			log.Warn("failed to persist engine status", "session", name, "error", err)
		}
	})
	reconnectCtx, reconnectCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := sessions.ReconnectActive(reconnectCtx); err != nil {
		log.Warn("failed to reconnect active sessions", "error", err)
	}
	reconnectCancel()

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.RequireAPIKey(api.NewRouterWithMedia(log, sessions, messages, webhooks, attachments), cfg.APIKey),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Attachments nobody fetched are removed after MediaTTL: once now, then every hour.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			if removed, err := attachments.Sweep(cfg.MediaTTL, time.Now()); err != nil {
				log.Warn("media sweep failed", "error", err)
			} else if removed > 0 {
				log.Info("media sweep", "removed", removed)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	go func() {
		log.Info("server starting", "addr", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown failed", "error", err)
		os.Exit(1)
	}
}
