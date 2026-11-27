package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/victor1mor/Zylpher/client"
	"github.com/victor1mor/Zylpher/internal/domain"
	"github.com/victor1mor/Zylpher/internal/media"
)

func TestGeneratedClientAgainstAPI(t *testing.T) {
	router, store, eng := newMediaRouter(t, 64)
	server := httptest.NewServer(http.StripPrefix("/zylpher", RequireAPIKey(router, "test-key")))
	defer server.Close()
	ctx := t.Context()
	sdk, err := client.New(server.URL+"/zylpher/", "test-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := client.New(server.URL+"/zylpher", "wrong-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = wrongKey.GetHealth(ctx)
	requireClientError(t, err, http.StatusUnauthorized, "unauthorized")

	if health, err := sdk.GetHealth(ctx); err != nil || health.Status != "ok" {
		t.Fatalf("health = %+v, err = %v", health, err)
	}
	if metrics, err := sdk.GetMetrics(ctx); err != nil || !strings.Contains(metrics, "zylpher_webhook_pending 0\n") {
		t.Fatalf("metrics = %q, err = %v", metrics, err)
	}
	const name = "generated-client"
	if created, err := sdk.CreateSession(ctx, client.CreateSessionRequest{Name: name}); err != nil ||
		created.Name != name || created.Status != "STOPPED" || created.CreatedAt.IsZero() {
		t.Fatalf("created = %+v, err = %v", created, err)
	}
	if started, err := sdk.StartSession(ctx, name); err != nil || started.Status != "WORKING" {
		t.Fatalf("started = %+v, err = %v", started, err)
	}
	if session, err := sdk.GetSession(ctx, name); err != nil || session.Name != name || session.Status != "WORKING" {
		t.Fatalf("session = %+v, err = %v", session, err)
	}
	if sessions, err := sdk.ListSessions(ctx); err != nil || len(sessions) != 2 || sessions[1].Name != name {
		t.Fatalf("sessions = %+v, err = %v", sessions, err)
	}
	_, err = sdk.GetSessionQR(ctx, name)
	requireClientError(t, err, http.StatusNotFound, "qr_unavailable")

	const lid = "12345@lid"
	if sent, err := sdk.SendText(ctx, name, client.SendTextRequest{To: lid, Text: "oi"}); err != nil ||
		sent.To != lid || sent.Status != "sent" || sent.Timestamp.IsZero() {
		t.Fatalf("text = %+v, err = %v", sent, err)
	}
	const mimeType = "audio/ogg; codecs=opus"
	params := client.SendMediaParams{To: lid, Kind: "audio", Voice: true}
	if sent, err := sdk.SendMedia(ctx, name, params, mimeType, strings.NewReader("OggS")); err != nil ||
		sent.To != lid || sent.Status != "sent" {
		t.Fatalf("media = %+v, err = %v", sent, err)
	}
	_, err = sdk.SendMedia(ctx, name, params, mimeType, strings.NewReader(strings.Repeat("x", 65)))
	requireClientError(t, err, http.StatusRequestEntityTooLarge, "media_too_large")

	info, err := store.Save(name, domain.ReceivedMedia{ID: "voice-1", Kind: domain.MediaAudio, MimeType: mimeType}, []byte("OggS"))
	if err != nil {
		t.Fatal(err)
	}
	checkClientDownload(t, ctx, sdk, name, info, "OggS")
	if err := sdk.DeleteMedia(ctx, name, info.ID); err != nil {
		t.Fatal(err)
	}
	_, err = sdk.DownloadMedia(ctx, name, info.ID)
	requireClientError(t, err, http.StatusNotFound, "not_found")
	if stopped, err := sdk.StopSession(ctx, name); err != nil || stopped.Status != "STOPPED" {
		t.Fatalf("stopped = %+v, err = %v", stopped, err)
	}
	if loggedOut, err := sdk.LogoutSession(ctx, name); err != nil || loggedOut.Status != "STOPPED" {
		t.Fatalf("logged out = %+v, err = %v", loggedOut, err)
	}
	server.Close()
	if !eng.last.Voice || eng.last.MimeType != mimeType || string(eng.last.Data) != "OggS" {
		t.Fatalf("upload reached engine as %+v", eng.last)
	}
}

func TestGeneratedClientAgainstService(t *testing.T) {
	baseURL := os.Getenv("ZYLPHER_SERVICE_TEST_URL")
	if baseURL == "" {
		t.Skip("set ZYLPHER_SERVICE_TEST_URL to test an installed service")
	}
	key := os.Getenv("ZYLPHER_API_KEY")
	sdk, err := client.New(baseURL, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	const name = "sdk-proof"
	const attachmentID = "sdk-attachment"
	t.Run("requests", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		dataDir := os.Getenv("ZYLPHER_DATA_DIR")
		if !filepath.IsAbs(dataDir) {
			t.Fatal("set ZYLPHER_DATA_DIR to the absolute data path of the isolated service")
		}
		if health, err := sdk.GetHealth(ctx); err != nil || health.Status != "ok" {
			t.Fatalf("health = %+v, err = %v", health, err)
		}
		wrongKey, err := client.New(baseURL, key+"-wrong", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = wrongKey.GetHealth(ctx)
		requireClientError(t, err, http.StatusUnauthorized, "unauthorized")
		metrics, err := sdk.GetMetrics(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, metric := range []string{"pending", "delivery_failures_total", "retries_total", "oldest_pending_age_seconds"} {
			if !strings.Contains(metrics, "\nzylpher_webhook_"+metric+" ") {
				t.Errorf("missing webhook metric %q", metric)
			}
		}
		if sessions, err := sdk.ListSessions(ctx); err != nil || len(sessions) != 0 {
			t.Fatalf("proof requires an empty session store, got %d sessions, err = %v", len(sessions), err)
		}
		input := client.CreateSessionRequest{Name: name}
		if session, err := sdk.CreateSession(ctx, input); err != nil || session.Name != name || session.Status != "STOPPED" {
			t.Fatalf("created session = %+v, err = %v", session, err)
		}
		_, err = sdk.CreateSession(ctx, input)
		requireClientError(t, err, http.StatusConflict, "conflict")
		_, err = sdk.StartSession(ctx, "sdk-missing")
		requireClientError(t, err, http.StatusNotFound, "not_found")
		_, err = sdk.GetSessionQR(ctx, name)
		requireClientError(t, err, http.StatusNotFound, "qr_unavailable")

		const lid = "12345@lid"
		_, err = sdk.SendText(ctx, name, client.SendTextRequest{To: lid, Text: "prova"})
		requireClientError(t, err, http.StatusConflict, "session_not_working")
		_, err = sdk.SendMedia(ctx, name, client.SendMediaParams{To: lid, Kind: "audio", Voice: true}, "audio/ogg", strings.NewReader("OggS"))
		requireClientError(t, err, http.StatusConflict, "session_not_working")

		const sample = "zylpher SDK attachment\x00\xff"
		store := media.NewStore(filepath.Join(dataDir, "media"), 1024)
		item, err := store.Save(name, domain.ReceivedMedia{ID: attachmentID, Kind: domain.MediaDocument, MimeType: "application/octet-stream"}, []byte(sample))
		if err != nil {
			t.Fatal(err)
		}
		checkClientDownload(t, ctx, sdk, name, item, sample)
		if err := sdk.DeleteMedia(ctx, name, attachmentID); err != nil {
			t.Fatal(err)
		}
		_, err = sdk.DownloadMedia(ctx, name, attachmentID)
		requireClientError(t, err, http.StatusNotFound, "not_found")
		if stopped, err := sdk.StopSession(ctx, name); err != nil || stopped.Status != "STOPPED" {
			t.Fatalf("stopped = %+v, err = %v", stopped, err)
		}
	})
	t.Run("after_restart", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		if session, err := sdk.GetSession(ctx, name); err != nil || session.Name != name || session.Status != "STOPPED" {
			t.Fatalf("persisted session = %+v, err = %v", session, err)
		}
		_, err := sdk.DownloadMedia(ctx, name, attachmentID)
		requireClientError(t, err, http.StatusNotFound, "not_found")
	})
}

func TestGeneratedClientFromAnotherModule(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	testGeneratedClientFromModule(t, fmt.Sprintf("%q", filepath.ToSlash(root)), "none", "")
}

const privateGitModule = "github.com/victor1mor/Zylpher"

func TestGeneratedClientFromPrivateGit(t *testing.T) {
	version := clientGitVersion(t, "ZYLPHER_CLIENT_GIT_VERSION", false)
	testGeneratedClientFromModule(t, privateGitModule+" "+version, privateGitModule, "")
}

func TestGeneratedClientFromCanonicalGit(t *testing.T) {
	version := clientGitVersion(t, "ZYLPHER_CANONICAL_CLIENT_GIT_VERSION", true)
	testGeneratedClientFromModule(t, "", privateGitModule, version)
}

func clientGitVersion(t *testing.T, variable string, allowLatest bool) string {
	t.Helper()
	version := os.Getenv(variable)
	if version == "" {
		t.Skipf("set %s to test an authenticated Git download", variable)
	}
	if allowLatest && version == "latest" {
		return version
	}
	if !strings.HasPrefix(version, "v") || strings.ContainsAny(version, " \t\r\n") {
		t.Fatalf("set %s to a Go version or pseudo-version", variable)
	}
	return version
}

func testGeneratedClientFromModule(t *testing.T, replacement, private, getVersion string) {
	t.Helper()
	const key = "test-consumer-key"
	server := httptest.NewServer(RequireAPIKey(newTestRouter(), key))
	defer server.Close()

	dir := t.TempDir()
	mod := "module example.com/zylpher-consumer\n\ngo 1.26.0\n"
	commands := [][]string{{"mod", "tidy"}, {"test", "-mod=readonly", "-count=1", "-v", "./..."}}
	importPath := "zylpher/client"
	if getVersion != "" {
		importPath = private + "/client"
		target := importPath
		if getVersion != "latest" {
			target += "@" + getVersion
		}
		commands = append([][]string{{"get", target}}, commands...)
	} else {
		mod += fmt.Sprintf("\nrequire zylpher v0.0.0\n\nreplace zylpher => %s\n", replacement)
	}
	const consumer = `package consumer

import (
	"os"
	"testing"

	"zylpher/client"
)

func TestClient(t *testing.T) {
	ctx := t.Context()
	sdk, err := client.New(os.Getenv("ZYLPHER_CONSUMER_URL"), os.Getenv("ZYLPHER_CONSUMER_KEY"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := sdk.GetHealth(ctx); err != nil || health.Status != "ok" {
		t.Fatalf("health = %+v, err = %v", health, err)
	}
	const name = "consumer"
	if _, err := sdk.CreateSession(ctx, client.CreateSessionRequest{Name: name}); err != nil {
		t.Fatal(err)
	}
	if started, err := sdk.StartSession(ctx, name); err != nil || started.Status != "WORKING" {
		t.Fatalf("started = %+v, err = %v", started, err)
	}
	const lid = "12345@lid"
	result, err := sdk.SendText(ctx, name, client.SendTextRequest{To: lid, Text: "prova"})
	if err != nil || result.To != lid || result.Status != "sent" {
		t.Fatalf("send = %+v, err = %v", result, err)
	}
}
`
	source := strings.Replace(consumer, `"zylpher/client"`, fmt.Sprintf("%q", importPath), 1)
	for name, contents := range map[string]string{"go.mod": mod, "client_test.go": source} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goTool += ".exe"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	for _, args := range commands {
		cmd := exec.CommandContext(ctx, goTool, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GOENV=off", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0",
			"GOPROXY=off", "GOPRIVATE="+private, "GONOPROXY="+private, "GONOSUMDB="+private,
			"GOMODCACHE="+filepath.Join(dir, "modcache"), "GIT_TERMINAL_PROMPT=0",
			"ZYLPHER_CONSUMER_URL="+server.URL, "ZYLPHER_CONSUMER_KEY="+key,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s em outro módulo: %v\n%s", strings.Join(args, " "), err, out)
		}
		if len(out) != 0 {
			t.Logf("%s", out)
		}
	}
}

func checkClientDownload(t *testing.T, ctx context.Context, sdk *client.Client, name string, item domain.ReceivedMedia, want string) {
	t.Helper()
	res, err := sdk.DownloadMedia(ctx, name, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil || string(data) != want || res.Header.Get("X-Zylpher-SHA256") != item.SHA256 ||
		res.Header.Get("Content-Type") != item.MimeType {
		t.Fatalf("download = %q, headers = %v, err = %v", data, res.Header, err)
	}
}

func requireClientError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var response *client.APIError
	if !errors.As(err, &response) || response.StatusCode != status || response.Code != code || response.Message == "" {
		t.Fatalf("error = %v, want HTTP %d with code %q and message", err, status, code)
	}
}
