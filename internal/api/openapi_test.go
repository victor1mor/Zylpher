package api

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/victor1mor/Zylpher/internal/domain"
)

func TestOpenAPIContract(t *testing.T) {
	type response struct {
		Ref     string `json:"$ref"`
		Content map[string]json.RawMessage
		Headers map[string]struct{ Required bool }
	}
	type operation struct {
		OperationID string
		Security    json.RawMessage
		Responses   map[string]response
		RequestBody struct {
			Required bool
			Content  map[string]json.RawMessage
		}
		Parameters []struct {
			Name     string
			In       string
			Required bool
		}
	}
	var spec struct {
		OpenAPI    string
		Security   []map[string][]string
		Paths      map[string]map[string]json.RawMessage
		Webhooks   map[string]map[string]operation
		Components struct {
			Responses       map[string]response
			SecuritySchemes map[string]struct{ Type, Scheme string }
		}
	}
	data, err := os.ReadFile("../../openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.1.") {
		t.Fatalf("expected OpenAPI 3.1, got %q", spec.OpenAPI)
	}
	bearer := spec.Components.SecuritySchemes["bearerAuth"]
	if len(spec.Security) != 1 || len(spec.Security[0]) != 1 || spec.Security[0]["bearerAuth"] == nil ||
		bearer.Type != "http" || bearer.Scheme != "bearer" {
		t.Fatal("OpenAPI must require the API's Bearer key")
	}
	hook := spec.Webhooks["webhookEvent"]["post"]
	var hookSecurity []map[string][]string
	if err := json.Unmarshal(hook.Security, &hookSecurity); err != nil || hookSecurity == nil || len(hookSecurity) != 0 ||
		len(hook.Parameters) != 1 ||
		hook.Parameters[0].Name != "X-Zylpher-Signature" || hook.Parameters[0].In != "header" || !hook.Parameters[0].Required {
		t.Fatal("webhooks must require the HMAC header without API Bearer authentication")
	}

	router, store, _ := newMediaRouter(t, 1024)
	if _, err := store.Save("default", domain.ReceivedMedia{ID: "attachment", Kind: domain.MediaImage, MimeType: "image/png"}, []byte("image")); err != nil {
		t.Fatal(err)
	}
	router = RequireAPIKey(router, "test-key")

	for _, tc := range []struct {
		method, path, target, body, contentType string
		status                                  int
	}{
		{"GET", "/health", "/health", "", "", 200},
		{"GET", "/metrics", "/metrics", "", "", 200},
		{"GET", "/sessions", "/sessions", "", "", 200},
		{"POST", "/sessions", "/sessions", `{"name":"second"}`, "application/json", 201},
		{"GET", "/sessions/{name}", "/sessions/default", "", "", 200},
		{"GET", "/sessions/{name}/qr", "/sessions/default/qr", "", "", 404},
		{"POST", "/sessions/{name}/start", "/sessions/second/start", "", "", 200},
		{"POST", "/sessions/{name}/stop", "/sessions/second/stop", "", "", 200},
		{"POST", "/sessions/{name}/messages/text", "/sessions/default/messages/text", `{"to":"12345@lid","text":"oi"}`, "application/json", 200},
		{"POST", "/sessions/{name}/messages/media", "/sessions/default/messages/media?to=12345%40lid&kind=image", "image", "image/png", 200},
		{"POST", "/sessions/{name}/messages/media", "/sessions/default/messages/media?to=12345%40lid&kind=image", strings.Repeat("x", 1025), "image/png", 413},
		{"GET", "/sessions/{name}/media/{id}", "/sessions/default/media/attachment", "", "", 200},
		{"DELETE", "/sessions/{name}/media/{id}", "/sessions/default/media/attachment", "", "", 204},
		{"GET", "/sessions/{name}/media/{id}", "/sessions/default/media/attachment", "", "", 404},
	} {
		t.Run(tc.method+" "+tc.path+"/"+strconv.Itoa(tc.status), func(t *testing.T) {
			var op operation
			if err := json.Unmarshal(spec.Paths[tc.path][strings.ToLower(tc.method)], &op); err != nil {
				t.Fatalf("undocumented operation: %v", err)
			}
			if op.OperationID == "" || op.Security != nil {
				t.Fatal("operation must have an ID and inherit Bearer authentication")
			}
			if tc.body != "" && (!op.RequestBody.Required || op.RequestBody.Content[tc.contentType] == nil) {
				t.Fatalf("undocumented request body of type %q", tc.contentType)
			}

			for _, status := range []int{http.StatusUnauthorized, tc.status} {
				req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", tc.contentType)
				if status != http.StatusUnauthorized {
					req.Header.Set("Authorization", "Bearer test-key")
				}
				res := httptest.NewRecorder()
				router.ServeHTTP(res, req)
				if res.Code != status {
					t.Fatalf("HTTP status = %d, want %d: %s", res.Code, status, res.Body)
				}
				doc, ok := op.Responses[strconv.Itoa(status)]
				if doc.Ref != "" {
					doc, ok = spec.Components.Responses[strings.TrimPrefix(doc.Ref, "#/components/responses/")]
				}
				if !ok {
					t.Fatalf("undocumented response for HTTP %d", status)
				}
				if status == http.StatusNoContent {
					if len(doc.Content) != 0 || res.Body.Len() != 0 {
						t.Fatal("204 must have no content")
					}
					continue
				}
				contentType, _, err := mime.ParseMediaType(res.Header().Get("Content-Type"))
				if err != nil || (doc.Content[contentType] == nil && doc.Content["*/*"] == nil) {
					t.Fatalf("undocumented Content-Type %q for HTTP %d", res.Header().Get("Content-Type"), status)
				}
				for name, header := range doc.Headers {
					if header.Required && res.Header().Get(name) == "" {
						t.Errorf("missing documented response header %q", name)
					}
				}
			}
		})
	}
}
