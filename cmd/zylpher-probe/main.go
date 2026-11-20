package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/engine"
)

const defaultDatabase = "data/whatsapp.db"

func main() {
	database := flag.String("db", defaultDatabase, "arquivo SQLite das credenciais")
	qrURL := flag.String("qr-url", "", "rota local que retorna o QR atual")
	flag.Parse()

	if *qrURL != "" {
		if err := renderQRURL(*qrURL, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "probe failed:", err)
			os.Exit(1)
		}
		return
	}

	if err := run(*database, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "probe failed:", err)
		os.Exit(1)
	}
}

func run(database string, out io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client, err := engine.NewWhatsApp(ctx, database)
	if err != nil {
		return err
	}
	defer client.Close()

	probeSession := domain.Session{Name: "probe"}
	if client.NeedsPairing() {
		if err := client.Pair(ctx, func(code string) {
			fmt.Fprintln(out, "scan this QR code in WhatsApp linked devices:")
			renderQR(out, code)
		}); err != nil {
			return err
		}
	} else if err := client.Connect(ctx, probeSession); err != nil {
		return err
	}

	fmt.Fprintln(out, "connected")
	fmt.Fprintln(out, "press Ctrl+C to disconnect")
	<-ctx.Done()
	return client.Disconnect(context.Background(), probeSession)
}

func renderQRURL(endpoint string, out io.Writer) error {
	uri, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse QR URL: %w", err)
	}
	if uri.Scheme != "http" || (uri.Hostname() != "127.0.0.1" && uri.Hostname() != "localhost" && uri.Hostname() != "::1") {
		return errors.New("QR URL must use local HTTP")
	}

	key := os.Getenv("ZYLPHER_API_KEY")
	if key == "" {
		return errors.New("ZYLPHER_API_KEY is required for QR URL")
	}
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create QR request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("get QR: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("get QR: %s", response.Status)
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode QR: %w", err)
	}
	if payload.Code == "" {
		return errors.New("QR code is empty")
	}
	renderQR(out, payload.Code)
	return nil
}

func renderQR(out io.Writer, code string) {
	qrterminal.GenerateWithConfig(code, qrterminal.Config{
		HalfBlocks: true,
		Level:      qrterminal.M,
		QuietZone:  2,
		Writer:     out,
	})
}
