package httpdelivery

import (
	"context"
	"net/http"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

// RouterDeps holds all dependencies for the HTTP router.
type RouterDeps struct {
	ReadyCheck          func(context.Context) error
	AccountRegistrar    AccountRegistrar
	Executor            Executor
	CodexPassthrough    CodexPassthrough
	ModelLister         ModelLister
	ModelResolver       ModelResolver
	AccountAcquirer     AccountAcquirer
	SessionLoader       SessionLoader
	LeaseReleaser       LeaseReleaser
	AccountLister       AccountLister
	AccountStatusSetter AccountStatusSetter
	RotationInspector   RotationInspector
	ForceModeManager    ForceModeManager
	QuotaRefresher      QuotaRefreshRunner
	Logger              HandlerLogger
	APIKey              string
	DefaultModel        string
}

// NewRouter wires the HTTP routes.
//
//	GET  /healthz              — process health
//	GET  /readyz               — storage readiness
//	POST /accounts             — register a ChatGPT Plus web account
//	GET  /v1/models            — OpenAI-compatible model list
//	POST /v1/chat/completions  — OpenAI-compatible chat (streaming + non-streaming)
func NewRouter(deps RouterDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", RootHandler())
	mux.HandleFunc("/v1", RootHandler())
	mux.HandleFunc("/healthz", method(http.MethodGet, HealthHandler()))
	mux.HandleFunc("/readyz", method(http.MethodGet, ReadyHandler(deps.ReadyCheck)))

	if deps.AccountRegistrar != nil {
		mux.HandleFunc("/accounts", method(http.MethodPost, auth(deps.APIKey, PostAccountHandler(deps.AccountRegistrar, deps.Logger))))
	}

	if deps.Executor != nil && deps.ModelLister != nil && deps.ModelResolver != nil && deps.AccountAcquirer != nil && deps.SessionLoader != nil && deps.LeaseReleaser != nil {
		modelsHandler := method(http.MethodGet, auth(deps.APIKey, GetOpenAIModelsHandler(deps.ModelLister)))
		mux.HandleFunc("/models", modelsHandler)
		mux.HandleFunc("/v1/models", modelsHandler)
		mux.HandleFunc("/v1/v1/models", modelsHandler)

		chatHandler := PostOpenAIChatCompletionsHandler(
			deps.ModelResolver,
			deps.Executor,
			deps.AccountAcquirer,
			deps.SessionLoader,
			deps.LeaseReleaser,
			deps.DefaultModel,
			deps.Logger,
		)
		protectedChatHandler := method(http.MethodPost, auth(deps.APIKey, chatHandler))
		mux.HandleFunc("/chat/completions", protectedChatHandler)
		mux.HandleFunc("/v1/chat/completions", protectedChatHandler)
		mux.HandleFunc("/v1/v1/chat/completions", protectedChatHandler)

		var codexPassthroughForResponses http.HandlerFunc
		if deps.CodexPassthrough != nil {
			codexPassthroughForResponses = PostCodexPassthroughHandler(
				deps.CodexPassthrough,
				deps.AccountAcquirer,
				deps.SessionLoader,
				deps.LeaseReleaser,
				deps.Logger,
			)
		}
		responsesHandler := method(http.MethodPost, auth(deps.APIKey, PostOpenAIResponsesHandler(chatHandler, codexPassthroughForResponses, deps.DefaultModel)))
		mux.HandleFunc("/responses", responsesHandler)
		mux.HandleFunc("/v1/responses", responsesHandler)
		mux.HandleFunc("/v1/v1/responses", responsesHandler)
	}

	if deps.CodexPassthrough != nil && deps.AccountAcquirer != nil && deps.SessionLoader != nil && deps.LeaseReleaser != nil {
		codexHandler := method(http.MethodPost, auth(deps.APIKey, PostCodexPassthroughHandler(
			deps.CodexPassthrough,
			deps.AccountAcquirer,
			deps.SessionLoader,
			deps.LeaseReleaser,
			deps.Logger,
		)))
		mux.HandleFunc("/backend-api/codex/responses", codexHandler)
		mux.HandleFunc("/v1/backend-api/codex/responses", codexHandler)
		mux.HandleFunc("/v1/v1/backend-api/codex/responses", codexHandler)
	}
	if deps.AccountLister != nil && deps.AccountStatusSetter != nil {
		mux.HandleFunc("/admin/accounts", method(http.MethodGet, auth(deps.APIKey, GetAdminAccountsHandler(deps.AccountLister))))
		mux.HandleFunc("/admin/accounts/{id}/disable", method(http.MethodPost, auth(deps.APIKey, PostAdminAccountStatusHandler(deps.AccountStatusSetter, domain.AccountRoutingDisabled, deps.Logger))))
		mux.HandleFunc("/admin/accounts/{id}/enable", method(http.MethodPost, auth(deps.APIKey, PostAdminAccountStatusHandler(deps.AccountStatusSetter, domain.AccountRoutingActive, deps.Logger))))
	}
	if deps.AccountLister != nil && deps.RotationInspector != nil {
		mux.HandleFunc("/admin/state", method(http.MethodGet, auth(deps.APIKey, GetAdminStateHandler(deps.AccountLister, deps.RotationInspector, deps.ForceModeManager))))
	}
	if deps.ForceModeManager != nil {
		mux.HandleFunc("/admin/force", method(http.MethodPost, auth(deps.APIKey, PostAdminForceModeHandler(deps.ForceModeManager, deps.Logger))))
	}
	if deps.QuotaRefresher != nil {
		mux.HandleFunc("/admin/quota/refresh", method(http.MethodPost, auth(deps.APIKey, PostAdminQuotaRefreshHandler(deps.QuotaRefresher, deps.AccountLister, deps.Logger))))
	}

	return mux
}

func auth(apiKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if apiKey == "" {
			next(w, r)
			return
		}

		if r.Header.Get("X-API-Key") == apiKey || r.Header.Get("Authorization") == "Bearer "+apiKey {
			next(w, r)
			return
		}

		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "unauthorized"})
	}
}

func method(expected string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != expected {
			w.Header().Set("Allow", expected)
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "method not allowed"})
			return
		}

		next(w, r)
	}
}
