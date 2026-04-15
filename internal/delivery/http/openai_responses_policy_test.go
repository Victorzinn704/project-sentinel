package httpdelivery

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDecideResponsesRoute(t *testing.T) {
	t.Run("defaults to translate", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		decision := decideResponsesRoute(req, []byte(`{"model":"gpt-5.4","input":"ping"}`), "sentinel-router")
		if decision.route != responsesRouteTranslate {
			t.Fatalf("route = %q, want %q", decision.route, responsesRouteTranslate)
		}
		if decision.reason != "default_translate" {
			t.Fatalf("reason = %q, want default_translate", decision.reason)
		}
	})

	t.Run("tools force passthrough", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		decision := decideResponsesRoute(req, []byte(`{"model":"gpt-5.4","input":"ping","tools":[{"type":"function"}]}`), "sentinel-router")
		if decision.route != responsesRoutePassthrough {
			t.Fatalf("route = %q, want %q", decision.route, responsesRoutePassthrough)
		}
		if decision.reason != "tools_present" {
			t.Fatalf("reason = %q, want tools_present", decision.reason)
		}
	})

	t.Run("env passthrough rewrites sentinel alias", func(t *testing.T) {
		t.Setenv(responsesRoutingModeEnv, "passthrough")
		t.Setenv(codexPassthroughModelEnv, "gpt-5.4")
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		decision := decideResponsesRoute(req, []byte(`{"model":"sentinel-router","input":"ping"}`), "sentinel-router")
		if decision.route != responsesRoutePassthrough {
			t.Fatalf("route = %q, want %q", decision.route, responsesRoutePassthrough)
		}
		if decision.reason != "env_passthrough+model_rewrite" {
			t.Fatalf("reason = %q, want env_passthrough+model_rewrite", decision.reason)
		}
		if got := string(decision.passthroughBody); got == `{"model":"sentinel-router","input":"ping"}` {
			t.Fatal("expected passthrough body to be rewritten")
		}
	})
}
