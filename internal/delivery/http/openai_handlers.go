package httpdelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
	"go.uber.org/zap"
)

const (
	openAIRequestLimitBytes  = 8 << 20
	defaultRateLimitCooldown = time.Hour
	forceAccountHeader       = "X-Sentinel-Force-Account"
)

func GetOpenAIModelsHandler(lister ModelLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		models := lister.Models()
		responseModels := make([]OpenAIModel, 0, len(models))
		for _, model := range models {
			ownedBy := model.OwnedBy
			if ownedBy == "" {
				ownedBy = "project-sentinel"
			}
			responseModels = append(responseModels, OpenAIModel{
				ID:      model.ID,
				Object:  "model",
				Created: 0,
				OwnedBy: ownedBy,
			})
		}

		writeJSON(w, http.StatusOK, OpenAIModelsResponse{
			Object: "list",
			Data:   responseModels,
		})
	}
}

// PostOpenAIChatCompletionsHandler handles POST /v1/chat/completions.
//
//  1. Parse the OpenAI request (basic validation)
//  2. Acquire an eligible ChatGPT Plus account lease
//  3. Load the account's encrypted session
//  4. Execute via the provider adapter
//  5. Release the lease with the appropriate outcome signal
//  6. Stream (or buffer) the translated response back to the IDE
func PostOpenAIChatCompletionsHandler(
	resolver ModelResolver,
	executor Executor,
	acquirer AccountAcquirer,
	sessions SessionLoader,
	releaser LeaseReleaser,
	defaultModel string,
	logger HandlerLogger,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		raw, err := readLimitedBody(w, r, openAIRequestLimitBytes)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error(), "invalid_body")
			return
		}

		var payload OpenAIChatCompletionRequest
		if err := json.Unmarshal(raw, &payload); err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON payload", "invalid_json")
			return
		}
		payload.Model = strings.TrimSpace(payload.Model)
		if payload.Model == "" {
			payload.Model = strings.TrimSpace(defaultModel)
			if payload.Model == "" {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model is required", "missing_model")
				return
			}
			raw, err = injectModelIntoJSONBody(raw, payload.Model)
			if err != nil {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON payload", "invalid_json")
				return
			}
		}
		if len(payload.Messages) == 0 {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "messages are required", "missing_messages")
			return
		}
		resolvedModel, ok := resolver.Resolve(payload.Model)
		if !ok {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "unknown model", "unknown_model")
			return
		}

		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = fmt.Sprintf("chatcmpl_%d", time.Now().UTC().UnixNano())
		}
		forcedAccountID := strings.TrimSpace(r.Header.Get(forceAccountHeader))
		if logger != nil {
			logger.Info(
				"openai compatible chat started",
				zap.String("request_id", requestID),
				zap.String("model", payload.Model),
				zap.String("provider", resolvedModel.Provider),
				zap.String("forced_account_id", forcedAccountID),
				zap.Bool("stream", payload.Stream),
			)
		}

		lease, account, err := acquirer.AcquireLease(r.Context(), domain.AccountLeaseRequest{
			RequestID:       requestID,
			Provider:        resolvedModel.Provider,
			ForcedAccountID: forcedAccountID,
		})
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "forced account not found", "forced_account_not_found")
				return
			}
			if errors.Is(err, domain.ErrNoEligibleAccounts) {
				if forcedAccountID != "" {
					writeOpenAIError(w, http.StatusConflict, "invalid_request_error", "forced account is not eligible", "forced_account_unavailable")
					return
				}
				writeOpenAIError(w, http.StatusServiceUnavailable, "server_error", "no eligible accounts available", "no_eligible_resources")
				return
			}
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to acquire account", "internal_error")
			return
		}

		released := false
		releaseLease := func() {
			if released {
				return
			}
			released = true
			if releaseErr := releaser.ReleaseLease(context.Background(), *lease); releaseErr != nil {
				if logger != nil {
					logger.Warn("lease release failed", zap.String("lease_id", lease.LeaseID), zap.String("error", releaseErr.Error()))
				}
			}
		}
		defer releaseLease()

		session, err := sessions.Load(account.AccountID)
		if err != nil {
			if logger != nil {
				logger.Error("session load failed", zap.String("account_id", account.AccountID), zap.String("error", err.Error()))
			}
			_ = releaser.RecordTransientFailure(r.Context(), account.AccountID)
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", "failed to load account session", "session_error")
			return
		}
		if session.Provider == "" {
			session.Provider = account.Provider
		}

		if logger != nil {
			logger.Info(
				"account acquired for request",
				zap.String("request_id", requestID),
				zap.String("account_id", account.AccountID),
				zap.String("provider", account.Provider),
			)
		}

		if payload.Stream {
			handleStreaming(w, r, executor, session, resolvedModel, requestID, raw, account.AccountID, releaser, logger, startedAt)
		} else {
			handleNonStreaming(w, r, executor, session, resolvedModel, requestID, raw, account.AccountID, releaser, logger, startedAt)
		}
	}
}

func handleStreaming(
	w http.ResponseWriter, r *http.Request,
	executor Executor,
	session *domain.Session,
	model domain.ResolvedModel,
	requestID string, raw []byte,
	accountID string,
	releaser LeaseReleaser,
	logger HandlerLogger,
	startedAt time.Time,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "streaming is not supported", "streaming_not_supported")
		return
	}

	streamStarted := false
	startStream := func() {
		if streamStarted {
			return
		}
		streamStarted = true
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
	}

	startStream()
	if err := writeInitialChatCompletionChunk(w, requestID, model.ID); err != nil {
		if logger != nil {
			logger.Warn("initial stream chunk write failed", zap.String("request_id", requestID), zap.String("error", err.Error()))
		}
		return
	}
	flusher.Flush()

	result, err := executor.Execute(r.Context(), requestID, session, model, raw, func(chunk []byte) error {
		startStream()
		_, writeErr := w.Write(chunk)
		if writeErr != nil {
			return writeErr
		}
		flusher.Flush()
		return nil
	})

	latencyMs := float64(time.Since(startedAt).Milliseconds())

	if err != nil {
		recordOutcome(r.Context(), releaser, accountID, err, result)
		status := httpStatusFromError(err)
		if !streamStarted {
			writeOpenAIError(w, status, openAIErrorType(err), err.Error(), openAIErrorCode(err))
			return
		}
		_ = writeSSEError(w, err.Error(), openAIErrorType(err), openAIErrorCode(err))
		flusher.Flush()
		return
	}

	recordQuotaSnapshot(r.Context(), releaser, accountID, result)
	_ = releaser.RecordSuccess(r.Context(), accountID, latencyMs)

	if logger != nil {
		logger.Info(
			"openai compatible chat completed",
			zap.String("request_id", requestID),
			zap.String("account_id", accountID),
			zap.Int("status_code", providerStatusCode(result)),
			zap.Duration("duration", time.Since(startedAt)),
		)
	}
}

func handleNonStreaming(
	w http.ResponseWriter, r *http.Request,
	executor Executor,
	session *domain.Session,
	model domain.ResolvedModel,
	requestID string, raw []byte,
	accountID string,
	releaser LeaseReleaser,
	logger HandlerLogger,
	startedAt time.Time,
) {
	var buffer bytes.Buffer
	result, err := executor.Execute(r.Context(), requestID, session, model, raw, func(chunk []byte) error {
		_, writeErr := buffer.Write(chunk)
		return writeErr
	})

	latencyMs := float64(time.Since(startedAt).Milliseconds())

	if err != nil {
		recordOutcome(r.Context(), releaser, accountID, err, result)
		writeOpenAIError(w, httpStatusFromError(err), openAIErrorType(err), err.Error(), openAIErrorCode(err))
		return
	}

	recordQuotaSnapshot(r.Context(), releaser, accountID, result)
	_ = releaser.RecordSuccess(r.Context(), accountID, latencyMs)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(buffer.Bytes()); err != nil && logger != nil {
		logger.Warn("openai compatible response write failed", zap.String("request_id", requestID), zap.String("error", err.Error()))
		return
	}

	if logger != nil {
		logger.Info(
			"openai compatible chat completed",
			zap.String("request_id", requestID),
			zap.String("account_id", accountID),
			zap.Int("status_code", providerStatusCode(result)),
			zap.Duration("duration", time.Since(startedAt)),
		)
	}
}

// recordOutcome translates adapter errors into per-account state signals.
// The upstream-declared Retry-After is honored when present; otherwise we fall
// back to a conservative 1h cooldown.
func recordOutcome(ctx context.Context, releaser LeaseReleaser, accountID string, err error, result *domain.ProviderResult) {
	recordQuotaSnapshot(ctx, releaser, accountID, result)
	switch {
	case errors.Is(err, domain.ErrPolicyRateLimit):
		retryAfter := defaultRateLimitCooldown
		if result != nil && result.RetryAfter > 0 {
			retryAfter = result.RetryAfter
		}
		_ = releaser.RecordRateLimit(ctx, accountID, int(retryAfter.Seconds()))
	case errors.Is(err, domain.ErrAuthFailure):
		_ = releaser.RecordAuthFailure(ctx, accountID)
	default:
		_ = releaser.RecordTransientFailure(ctx, accountID)
	}
}

func recordQuotaSnapshot(ctx context.Context, releaser LeaseReleaser, accountID string, result *domain.ProviderResult) {
	if result == nil || result.QuotaSnapshot == nil {
		return
	}
	_ = releaser.RecordQuotaSnapshot(ctx, accountID, *result.QuotaSnapshot)
}

// ============================================================================
// Helpers
// ============================================================================

func readLimitedBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, errors.New("request body is required")
	}

	return raw, nil
}

func injectModelIntoJSONBody(raw []byte, model string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	payload["model"] = model
	return json.Marshal(payload)
}

func writeOpenAIError(w http.ResponseWriter, status int, errorType string, message string, code string) {
	if message == "" {
		message = http.StatusText(status)
	}
	if errorType == "" {
		errorType = "server_error"
	}

	writeJSON(w, status, OpenAIErrorEnvelope{
		Error: OpenAIError{
			Message: message,
			Type:    errorType,
			Code:    code,
		},
	})
}

func writeSSEError(w http.ResponseWriter, message string, errorType string, code string) error {
	payload, err := json.Marshal(OpenAIErrorEnvelope{
		Error: OpenAIError{
			Message: message,
			Type:    errorType,
			Code:    code,
		},
	})
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	_, err = fmt.Fprint(w, "data: [DONE]\n\n")
	return err
}

func writeInitialChatCompletionChunk(w io.Writer, requestID string, model string) error {
	payload, err := json.Marshal(map[string]any{
		"id":      "chatcmpl-" + requestID,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]any{
					"role": "assistant",
				},
				"finish_reason": nil,
			},
		},
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", payload)
	return err
}

func providerStatusCode(result *domain.ProviderResult) int {
	if result == nil {
		return 0
	}
	return result.StatusCode
}

func openAIErrorType(err error) string {
	switch {
	case errors.Is(err, domain.ErrInvalidData):
		return "invalid_request_error"
	case errors.Is(err, domain.ErrNoEligibleAccounts):
		return "server_error"
	case errors.Is(err, domain.ErrPolicyRateLimit):
		return "rate_limit_error"
	case errors.Is(err, domain.ErrAuthFailure):
		return "authentication_error"
	default:
		return "server_error"
	}
}

func openAIErrorCode(err error) string {
	switch {
	case errors.Is(err, domain.ErrInvalidData):
		return "invalid_request"
	case errors.Is(err, domain.ErrNoEligibleAccounts):
		return "no_eligible_resources"
	case errors.Is(err, domain.ErrPolicyRateLimit):
		return "rate_limit"
	case errors.Is(err, domain.ErrAuthFailure):
		return "auth_failure"
	case errors.Is(err, domain.ErrTransientUpstream):
		return "upstream_unavailable"
	default:
		return "internal_error"
	}
}
