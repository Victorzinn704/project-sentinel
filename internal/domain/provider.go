package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	ProviderChatGPT = "chatgpt"
	ProviderClaude  = "claude"
	ProviderGemini  = "gemini"
)

type ResolvedModel struct {
	ID            string
	Provider      string
	UpstreamModel string
	Capabilities  []string
	OwnedBy       string
}

type AccountLeaseRequest struct {
	RequestID       string
	Provider        string
	ForcedAccountID string
}

type ForceModeState struct {
	Active    bool
	AccountID string
	UpdatedAt time.Time
}

func NormalizeProvider(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", ProviderChatGPT, "openai", "chatgpt-web", "chatgpt_web", "codex", "openai-web":
		return ProviderChatGPT, nil
	case ProviderClaude, "anthropic":
		return ProviderClaude, nil
	case ProviderGemini, "google", "google-ai", "google-ai-studio", "aistudio":
		return ProviderGemini, nil
	default:
		return "", fmt.Errorf("unknown provider %q", raw)
	}
}
