package httpdelivery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
	"go.uber.org/zap"
)

const codexPassthroughRequestLimitBytes = 16 << 20

// CodexPassthrough forwards a Codex CLI request verbatim to chatgpt.com and
// streams the raw SSE response back. No payload translation is performed.
type CodexPassthrough interface {
	ProxyCodexRequest(
		ctx context.Context,
		requestID string,
		session *domain.Session,
		rawBody []byte,
		chunkWriter func([]byte) error,
	) (*domain.ProviderResult, error)
}

// PostCodexPassthroughHandler exposes a native Codex backend endpoint at
// /backend-api/codex/responses. Codex CLI patched to point at Sentinel hits
// this route, Sentinel picks a ChatGPT account via the rotation strategy,
// and the request is proxied byte-for-byte to chatgpt.com using that
// account's session credentials.
//
// Lease acquire/release and outcome recording happen here; the adapter only
// does the upstream I/O.
func PostCodexPassthroughHandler(
	proxy CodexPassthrough,
	acquirer AccountAcquirer,
	sessions SessionLoader,
	releaser LeaseReleaser,
	logger HandlerLogger,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()

		raw, err := readLimitedBody(w, r, codexPassthroughRequestLimitBytes)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error(), "invalid_body")
			return
		}

		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = fmt.Sprintf("codex_%d", time.Now().UTC().UnixNano())
		}
		forcedAccountID := strings.TrimSpace(r.Header.Get(forceAccountHeader))

		if logger != nil {
			logger.Info(
				"codex passthrough started",
				zap.String("request_id", requestID),
				zap.String("forced_account_id", forcedAccountID),
				zap.Int("body_bytes", len(raw)),
			)
		}

		lease, account, err := acquirer.AcquireLease(r.Context(), domain.AccountLeaseRequest{
			RequestID:       requestID,
			Provider:        domain.ProviderChatGPT,
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
				"account acquired for codex passthrough",
				zap.String("request_id", requestID),
				zap.String("account_id", account.AccountID),
			)
		}

		flusher, _ := w.(http.Flusher)
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

		result, err := proxy.ProxyCodexRequest(r.Context(), requestID, session, raw, func(chunk []byte) error {
			startStream()
			if _, writeErr := w.Write(chunk); writeErr != nil {
				return writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
			return nil
		})

		latencyMs := float64(time.Since(startedAt).Milliseconds())

		if err != nil {
			recordOutcome(r.Context(), releaser, account.AccountID, err, result)
			if !streamStarted {
				writeOpenAIError(w, httpStatusFromError(err), openAIErrorType(err), err.Error(), openAIErrorCode(err))
			}
			if logger != nil {
				logger.Warn(
					"codex passthrough failed",
					zap.String("request_id", requestID),
					zap.String("account_id", account.AccountID),
					zap.Int("status_code", providerStatusCode(result)),
					zap.String("error", err.Error()),
				)
			}
			return
		}

		recordQuotaSnapshot(r.Context(), releaser, account.AccountID, result)
		_ = releaser.RecordSuccess(r.Context(), account.AccountID, latencyMs)

		if logger != nil {
			logger.Info(
				"codex passthrough completed",
				zap.String("request_id", requestID),
				zap.String("account_id", account.AccountID),
				zap.Int("status_code", providerStatusCode(result)),
				zap.Duration("duration", time.Since(startedAt)),
			)
		}
	}
}
