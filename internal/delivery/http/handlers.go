package httpdelivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/seu-usuario/project-sentinel/internal/domain"
	"go.uber.org/zap"
)

// AccountRegistrar handles new account (ChatGPT session) registration.
type AccountRegistrar interface {
	RegisterAccount(ctx context.Context, account domain.Account) error
}

// ModelLister exposes the configured logical models.
type ModelLister interface {
	Models() []domain.ModelInfo
}

type ModelResolver interface {
	Resolve(modelID string) (domain.ResolvedModel, bool)
}

// Executor performs an upstream provider round-trip. Lease acquire/release
// is the HTTP handler's responsibility; this only does I/O.
type Executor interface {
	Execute(
		ctx context.Context,
		requestID string,
		session *domain.Session,
		model domain.ResolvedModel,
		rawBody []byte,
		streamWriter func([]byte) error,
	) (*domain.ProviderResult, error)
}

// SessionLoader loads an encrypted session by account ID.
type SessionLoader interface {
	Load(accountID string) (*domain.Session, error)
}

// AccountAcquirer selects the best eligible account and returns a lease.
type AccountAcquirer interface {
	AcquireLease(ctx context.Context, request domain.AccountLeaseRequest) (*domain.Lease, *domain.AccountState, error)
}

// LeaseReleaser releases a lease and records per-account outcome signals.
type LeaseReleaser interface {
	ReleaseLease(ctx context.Context, lease domain.Lease) error
	RecordSuccess(ctx context.Context, accountID string, latencyMs float64) error
	RecordRateLimit(ctx context.Context, accountID string, retryAfterSeconds int) error
	RecordAuthFailure(ctx context.Context, accountID string) error
	RecordTransientFailure(ctx context.Context, accountID string) error
	RecordQuotaSnapshot(ctx context.Context, accountID string, snapshot domain.AccountQuotaSnapshot) error
}

type HandlerLogger interface {
	Info(message string, fields ...zap.Field)
	Warn(message string, fields ...zap.Field)
	Error(message string, fields ...zap.Field)
}

func RootHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/v1" {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "not found",
				"hint":  "Use base URL http://127.0.0.1:8080/v1 for OpenAI-compatible clients.",
				"routes": []string{
					"GET /v1/models",
					"POST /v1/chat/completions",
					"POST /v1/responses",
					"GET /admin/accounts",
					"GET /admin/state",
				},
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"name":   "project-sentinel",
			"status": "ok",
			"openai_compatible": map[string]any{
				"base_url":  "http://127.0.0.1:8080/v1",
				"models":    "/v1/models",
				"chat":      "/v1/chat/completions",
				"responses": "/v1/responses",
			},
		})
	}
}

func HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
	}
}

func ReadyHandler(check func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			if err := check(r.Context()); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: err.Error()})
				return
			}
		}

		writeJSON(w, http.StatusOK, HealthResponse{Status: "ready"})
	}
}

// PostAccountHandler registers a new ChatGPT Plus web account as a resource.
func PostAccountHandler(registrar AccountRegistrar, logger HandlerLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		var payload AccountRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid JSON payload"})
			return
		}

		payload.AccountID = strings.TrimSpace(payload.AccountID)
		payload.Provider = strings.TrimSpace(payload.Provider)
		payload.Email = strings.TrimSpace(payload.Email)
		payload.AuthToken = strings.TrimSpace(payload.AuthToken)
		payload.UserAgent = strings.TrimSpace(payload.UserAgent)

		if payload.Provider == "" {
			payload.Provider = domain.ProviderChatGPT
		}

		accountID := payload.AccountID
		if accountID == "" || accountID == "0" {
			if payload.Email == "" {
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "id or email is required"})
				return
			}
			accountID = accountIDFromEmail(payload.Email)
		}
		if payload.Email == "" {
			payload.Email = accountID
		}

		authParams := make(map[string]string, len(payload.AuthParams))
		for k, v := range payload.AuthParams {
			authParams[k] = v
		}

		account := domain.Account{
			ID:          accountID,
			Provider:    payload.Provider,
			Email:       payload.Email,
			AccessToken: payload.AuthToken,
			DisplayName: payload.Email,
			Status:      domain.AccountActive,
			Region:      "br",
			UserAgent:   payload.UserAgent,
			AuthParams:  authParams,
		}

		if err := registrar.RegisterAccount(r.Context(), account); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, domain.ErrInvalidData) {
				status = http.StatusBadRequest
			}
			if errors.Is(err, domain.ErrAccountAlreadyExists) {
				status = http.StatusConflict
			}

			if logger != nil {
				logger.Warn(
					"account registration failed",
					zap.String("account_id", accountID),
					zap.Int("status_code", status),
					zap.String("error", err.Error()),
				)
			}

			writeJSON(w, status, ErrorResponse{Error: err.Error()})
			return
		}

		if logger != nil {
			logger.Info("account registered", zap.String("account_id", account.ID))
		}

		writeJSON(w, http.StatusCreated, AccountResponse{
			Success:   true,
			Message:   "account registered",
			AccountID: account.ID,
		})
	}
}

func accountIDFromEmail(email string) string {
	var builder strings.Builder
	builder.WriteString("acc_")

	for _, char := range strings.ToLower(email) {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case unicode.IsDigit(char):
			builder.WriteRune(char)
		case char == '_' || char == '-':
			builder.WriteRune(char)
		default:
			builder.WriteRune('_')
		}
	}

	return builder.String()
}

func httpStatusFromError(err error) int {
	switch {
	case errors.Is(err, domain.ErrInvalidData):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrNoEligibleAccounts):
		return http.StatusServiceUnavailable
	case errors.Is(err, domain.ErrPolicyRateLimit):
		return http.StatusTooManyRequests
	case errors.Is(err, domain.ErrAuthFailure):
		return http.StatusForbidden
	case errors.Is(err, domain.ErrTransientUpstream):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
