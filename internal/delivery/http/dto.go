package httpdelivery

import "encoding/json"

// AccountRequest matches the JSON structure directly extracted from the browser.
type AccountRequest struct {
	AccountID      string            `json:"id,omitempty"`
	Provider       string            `json:"provider"`
	Email          string            `json:"email"`
	Status         string            `json:"status,omitempty"`
	CooldownUntil  string            `json:"cooldown_until,omitempty"`
	AuthToken      string            `json:"auth_token"`
	UserAgent      string            `json:"user_agent"`
	AuthParams     map[string]string `json:"auth_params,omitempty"`
}

type AccountSentinelTokens struct {
	ChatRequirements string `json:"chat_requirements,omitempty"`
	Proof            string `json:"proof,omitempty"`
	Turnstile        string `json:"turnstile,omitempty"`
}

type AccountResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	AccountID string `json:"account_id"`
}

type HealthResponse struct {
	Status string `json:"status"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

// OpenAI-compatible types (used by /v1/chat/completions and /v1/models)

type OpenAIChatCompletionRequest struct {
	Model    string            `json:"model"`
	Messages []json.RawMessage `json:"messages"`
	Stream   bool              `json:"stream,omitempty"`
}

type OpenAIModelsResponse struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}

type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type OpenAIErrorEnvelope struct {
	Error OpenAIError `json:"error"`
}

type OpenAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}
