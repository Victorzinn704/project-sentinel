package httpdelivery

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

type responsesRoute string

const (
	responsesRouteTranslate   responsesRoute = "translate"
	responsesRoutePassthrough responsesRoute = "passthrough"
)

type responsesRouteDecision struct {
	route           responsesRoute
	reason          string
	passthroughBody []byte
	hasTools        bool
}

func decideResponsesRoute(r *http.Request, raw []byte, defaultModel string) responsesRouteDecision {
	decision := responsesRouteDecision{
		route:           responsesRouteTranslate,
		reason:          "default_translate",
		passthroughBody: raw,
		hasTools:        responsesRequestHasTools(raw),
	}

	switch configuredResponsesRoutingMode() {
	case responsesRoutePassthrough:
		decision.route = responsesRoutePassthrough
		decision.reason = "env_passthrough"
	case responsesRouteTranslate:
		decision.reason = "env_translate"
	default:
		switch {
		case isCodexCLIRequest(r):
			decision.route = responsesRoutePassthrough
			decision.reason = "codex_cli_header"
		case decision.hasTools:
			decision.route = responsesRoutePassthrough
			decision.reason = "tools_present"
		}
	}

	if decision.route == responsesRoutePassthrough {
		var rewritten bool
		decision.passthroughBody, rewritten = normalizeCodexPassthroughModel(raw, defaultModel)
		if rewritten {
			decision.reason += "+model_rewrite"
		}
	}

	return decision
}

func configuredResponsesRoutingMode() responsesRoute {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(responsesRoutingModeEnv)))
	switch mode {
	case "passthrough", "native", "force_passthrough":
		return responsesRoutePassthrough
	case "translate", "force_translate", "compat":
		return responsesRouteTranslate
	default:
		return ""
	}
}

func shouldUseCodexPassthrough(r *http.Request, raw []byte) (bool, string) {
	decision := decideResponsesRoute(r, raw, "")
	return decision.route == responsesRoutePassthrough, decision.reason
}

func responsesRequestHasTools(raw []byte) bool {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return false
	}
	toolsRaw, ok := body["tools"]
	if !ok {
		return false
	}

	trimmed := bytes.TrimSpace(toolsRaw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("[]")) {
		return false
	}

	var tools []json.RawMessage
	if err := json.Unmarshal(trimmed, &tools); err == nil {
		return len(tools) > 0
	}

	return true
}
