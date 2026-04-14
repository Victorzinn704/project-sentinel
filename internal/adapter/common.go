package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

func resolveBaseURL(session *domain.Session, fallback string) string {
	if session == nil || session.AuthParams == nil {
		return fallback
	}

	for _, key := range []string{"base_url", "baseURL", "BASE_URL"} {
		if value := strings.TrimSpace(session.AuthParams[key]); value != "" {
			return value
		}
	}

	return fallback
}

func providerToken(session *domain.Session) string {
	if session == nil {
		return ""
	}
	if token := strings.TrimSpace(session.AccessToken); token != "" {
		return token
	}
	if session.AuthParams == nil {
		return ""
	}
	for _, key := range []string{"api_key", "apiKey", "API_KEY"} {
		if value := strings.TrimSpace(session.AuthParams[key]); value != "" {
			return value
		}
	}
	return ""
}

func sessionAccountID(session *domain.Session) string {
	if session == nil {
		return ""
	}
	return session.AccountID
}

func handleProviderStatus(resp *http.Response, result *domain.ProviderResult) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests:
		result.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		return domain.ErrPolicyRateLimit
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return domain.ErrAuthFailure
	case resp.StatusCode >= 500 && resp.StatusCode <= 599:
		return fmt.Errorf("%w: upstream returned status %d", domain.ErrTransientUpstream, resp.StatusCode)
	default:
		message := strings.TrimSpace(readProviderErrorMessage(resp.Body))
		if message == "" {
			message = fmt.Sprintf("upstream returned status %d", resp.StatusCode)
		}
		return fmt.Errorf("%w: %s", domain.ErrInvalidData, message)
	}
}

func readProviderErrorMessage(body io.Reader) string {
	if body == nil {
		return ""
	}

	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return ""
	}
	if strings.TrimSpace(payload.Error.Message) != "" {
		return payload.Error.Message
	}
	return payload.Message
}

func writeOpenAITextResponse(requestID string, model string, content string, streamWriter func([]byte) error) error {
	responseBytes, err := json.Marshal(map[string]any{
		"id":      openAICompletionID(requestID),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	})
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}

	return streamWriter(responseBytes)
}

func openAICompletionID(requestID string) string {
	return "chatcmpl-" + requestID
}
