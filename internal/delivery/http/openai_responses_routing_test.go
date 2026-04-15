package httpdelivery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostOpenAIResponsesHandlerRoutesCodexHeadersToPassthrough(t *testing.T) {
	chatCalled := false
	passthroughCalled := false

	handler := PostOpenAIResponsesHandler(
		func(http.ResponseWriter, *http.Request) {
			chatCalled = true
		},
		func(w http.ResponseWriter, r *http.Request) {
			passthroughCalled = true
			if r.URL.Path != "/backend-api/codex/responses" {
				t.Fatalf("expected rewritten codex path, got %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		},
		"sentinel-router",
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","input":"ping"}`))
	req.Header.Set("Originator", "codex_cli_rs")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !passthroughCalled {
		t.Fatal("expected passthrough handler to be called")
	}
	if chatCalled {
		t.Fatal("did not expect chat translation handler to be called")
	}
	if got := rr.Header().Get("X-Sentinel-Responses-Route"); got != "passthrough" {
		t.Fatalf("expected route header passthrough, got %q", got)
	}
}

func TestPostOpenAIResponsesHandlerRoutesToolsToPassthrough(t *testing.T) {
	chatCalled := false
	passthroughCalled := false

	handler := PostOpenAIResponsesHandler(
		func(http.ResponseWriter, *http.Request) {
			chatCalled = true
		},
		func(w http.ResponseWriter, r *http.Request) {
			passthroughCalled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		},
		"sentinel-router",
	)

	reqBody := `{"model":"gpt-5.4","input":"ping","tools":[{"type":"function","name":"run_in_terminal"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !passthroughCalled {
		t.Fatal("expected passthrough handler to be called when tools are present")
	}
	if chatCalled {
		t.Fatal("did not expect chat translation handler to be called when tools are present")
	}
	if got := rr.Header().Get("X-Sentinel-Responses-Reason"); got != "tools_present" {
		t.Fatalf("expected reason tools_present, got %q", got)
	}
}

func TestPostOpenAIResponsesHandlerRewritesSentinelRouterModelForPassthrough(t *testing.T) {
	t.Setenv(responsesRoutingModeEnv, "passthrough")
	t.Setenv(codexPassthroughModelEnv, "gpt-5.4")

	rewrittenModel := ""
	handler := PostOpenAIResponsesHandler(
		func(http.ResponseWriter, *http.Request) {
			t.Fatal("did not expect translate handler in forced passthrough mode")
		},
		func(w http.ResponseWriter, r *http.Request) {
			body := make(map[string]json.RawMessage)
			decoded, err := readLimitedBody(w, r, 1<<20)
			if err != nil {
				t.Fatalf("failed to read rewritten body: %v", err)
			}
			if err := json.Unmarshal(decoded, &body); err != nil {
				t.Fatalf("failed to decode rewritten body: %v", err)
			}
			rewrittenModel = rawString(body["model"])
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		},
		"sentinel-router",
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"sentinel-router","input":"ping","tools":[{"type":"function","name":"run_in_terminal"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rewrittenModel != "gpt-5.4" {
		t.Fatalf("expected rewritten model gpt-5.4, got %q", rewrittenModel)
	}
	if got := rr.Header().Get("X-Sentinel-Responses-Reason"); got != "env_passthrough+model_rewrite" {
		t.Fatalf("expected rewrite reason, got %q", got)
	}
}

func TestPostOpenAIResponsesHandlerUsesTranslateByDefault(t *testing.T) {
	chatCalled := false
	passthroughCalled := false

	handler := PostOpenAIResponsesHandler(
		func(w http.ResponseWriter, r *http.Request) {
			chatCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl_test","created":1713110400,"model":"sentinel-router","choices":[{"message":{"content":"ok","role":"assistant"},"finish_reason":"stop"}]}`))
		},
		func(http.ResponseWriter, *http.Request) {
			passthroughCalled = true
		},
		"sentinel-router",
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","input":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !chatCalled {
		t.Fatal("expected translate handler to be called by default")
	}
	if passthroughCalled {
		t.Fatal("did not expect passthrough handler by default")
	}
	if got := rr.Header().Get("X-Sentinel-Responses-Route"); got != "translate" {
		t.Fatalf("expected route header translate, got %q", got)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
}

func TestPostOpenAIResponsesHandlerEnvForcesPassthrough(t *testing.T) {
	t.Setenv(responsesRoutingModeEnv, "passthrough")

	chatCalled := false
	passthroughCalled := false

	handler := PostOpenAIResponsesHandler(
		func(http.ResponseWriter, *http.Request) {
			chatCalled = true
		},
		func(w http.ResponseWriter, r *http.Request) {
			passthroughCalled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		},
		"sentinel-router",
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"sentinel-router","input":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !passthroughCalled {
		t.Fatal("expected passthrough handler to be called with forced mode")
	}
	if chatCalled {
		t.Fatal("did not expect translate handler in forced passthrough mode")
	}
	if got := rr.Header().Get("X-Sentinel-Responses-Reason"); !strings.HasPrefix(got, "env_passthrough") {
		t.Fatalf("expected reason starting with env_passthrough, got %q", got)
	}
}
