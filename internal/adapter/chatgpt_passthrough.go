package adapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/google/uuid"
	"github.com/seu-usuario/project-sentinel/internal/domain"
)

// ProxyCodexRequest forwards a raw Codex CLI request to chatgpt.com without
// any payload translation. The request body and response stream are passed
// through verbatim, preserving the native Codex SSE event types (tool calls,
// reasoning, output_text deltas) that the CLI parser expects.
//
// This bypasses the OpenAI-compat translation pipeline used by Execute. Use
// it when the client speaks the Codex backend protocol natively and only
// needs Sentinel for account multiplexing / lease management.
func (a *ChatGPTAdapter) ProxyCodexRequest(
	ctx context.Context,
	requestID string,
	session *domain.Session,
	rawBody []byte,
	chunkWriter func([]byte) error,
) (*domain.ProviderResult, error) {

	reqURL := resolveChatGPTURL(session)
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodPost, reqURL, bytes.NewReader(rawBody))
	if err != nil {
		return nil, fmt.Errorf("create upstream request: %w", err)
	}

	req.Header = fhttp.Header{
		"content-type": {"application/json"},
		"accept":       {"text/event-stream"},
		"originator":   {"codex_cli_rs"},
		"version":      {"0.101.0"},
		"user-agent":   {"codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"},
		"connection":   {"Keep-Alive"},
		"session_id":   {uuid.NewString()},
	}
	if accountID := chatGPTAccountID(session); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	injectSessionCredentials(req, session)

	resp, err := a.client.Do(req)
	if err != nil {
		return &domain.ProviderResult{
			RequestID:  requestID,
			ResourceID: session.AccountID,
		}, fmt.Errorf("%w: %v", domain.ErrTransientUpstream, err)
	}
	defer resp.Body.Close()

	result := &domain.ProviderResult{
		RequestID:  requestID,
		ResourceID: session.AccountID,
		StatusCode: resp.StatusCode,
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if readErr != nil {
			return result, fmt.Errorf("%w: read upstream error body: %v", domain.ErrTransientUpstream, readErr)
		}
		result.QuotaSnapshot = extractRuntimeQuotaSnapshotFromPayload(raw, time.Now().UTC())

		// Forward the upstream error body verbatim so the client can inspect
		// the structured error response from chatgpt.com.
		if len(raw) > 0 {
			if writeErr := chunkWriter(raw); writeErr != nil {
				return result, writeErr
			}
		}

		switch {
		case resp.StatusCode == 429:
			result.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
			if result.RetryAfter <= 0 {
				result.RetryAfter = parseCodexRetryAfter(bytes.NewReader(raw))
			}
			return result, domain.ErrPolicyRateLimit
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return result, domain.ErrAuthFailure
		case resp.StatusCode >= 500 && resp.StatusCode <= 599:
			return result, fmt.Errorf("%w: upstream returned status %d", domain.ErrTransientUpstream, resp.StatusCode)
		default:
			return result, fmt.Errorf("%w: upstream returned status %d", domain.ErrInvalidData, resp.StatusCode)
		}
	}

	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if writeErr := chunkWriter(buf[:n]); writeErr != nil {
				return result, writeErr
			}
		}
		if readErr == io.EOF {
			return result, nil
		}
		if readErr != nil {
			return result, fmt.Errorf("%w: read upstream stream: %v", domain.ErrTransientUpstream, readErr)
		}
	}
}
