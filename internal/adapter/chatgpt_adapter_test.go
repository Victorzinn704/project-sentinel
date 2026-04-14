package adapter

import (
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/seu-usuario/project-sentinel/internal/domain"
)

func TestShouldDropSessionHeaderBlocksLocalControlPlane(t *testing.T) {
	blocked := []string{
		"Authorization",
		"X-API-Key",
		"X-Sentinel-Force-Account",
		"Sentinel-Api-Key",
		"CODEX_API_KEY",
		"CODEX_BASE_URL",
		"OpenAI-Api-Key",
		"Proxy-Authorization",
		"Transfer-Encoding",
	}

	for _, header := range blocked {
		if !shouldDropSessionHeader(header) {
			t.Fatalf("expected %q to be blocked", header)
		}
	}

	if shouldDropSessionHeader("X-Project-Trace") {
		t.Fatal("expected X-Project-Trace to be allowed")
	}
}

func TestInjectSessionCredentialsDoesNotLeakLocalHeaders(t *testing.T) {
	req, err := fhttp.NewRequest(fhttp.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("authorization", "Bearer original")
	req.Header.Set("originator", "codex_cli_rs")

	injectSessionCredentials(req, &domain.Session{
		AccessToken: "session-token",
		AuthParams: map[string]string{
			"authorization":            "Bearer leaked",
			"raw_cookies":              "a=b",
			"X-API-Key":                "local-key",
			"X-Sentinel-Force-Account": "acc_1",
			"CODEX_API_KEY":            "codex-key",
			"Proxy-Authorization":      "proxy-secret",
			"originator":               "bad-originator",
			"X-Project-Trace":          "trace-1",
		},
	})

	if got := req.Header.Get("authorization"); got != "Bearer session-token" {
		t.Fatalf("authorization = %q, want session token", got)
	}
	if got := req.Header.Get("cookie"); got != "a=b" {
		t.Fatalf("cookie = %q, want raw cookies", got)
	}
	if got := req.Header.Get("originator"); got != "codex_cli_rs" {
		t.Fatalf("originator = %q, want protected original value", got)
	}
	if got := req.Header.Get("X-Project-Trace"); got != "trace-1" {
		t.Fatalf("X-Project-Trace = %q, want allowed custom header", got)
	}

	for _, leaked := range []string{"X-API-Key", "X-Sentinel-Force-Account", "CODEX_API_KEY", "Proxy-Authorization"} {
		if got := req.Header.Get(leaked); got != "" {
			t.Fatalf("%s leaked as %q", leaked, got)
		}
	}
}
