package domain

import (
	"errors"
	"time"
)

var (
	ErrAccountAlreadyExists = errors.New("account already exists")
	ErrInvalidData          = errors.New("invalid data")
)

type AccountStatus string

const (
	AccountActive   AccountStatus = "active"
	AccountDisabled AccountStatus = "disabled"
)

// Account represents a ChatGPT Plus web account used as a resource.
type Account struct {
	ID               string            `json:"id"`
	Provider         string            `json:"provider"`                              // Multi-provider sphere identifier (e.g., openai, gemini, claude)
	Email            string            `json:"email"`
	AccessToken      string            `json:"-"`                            // Bearer token; never serialized
	OrganizationID   string            `json:"organization_id"`
	DisplayName      string            `json:"display_name"`
	Status           AccountStatus     `json:"status"`
	Region           string            `json:"region"`
	UserAgent        string            `json:"user_agent,omitempty"`         // Exact UA from extraction
	AuthParams       map[string]string `json:"auth_params,omitempty"`        // Replaces RawCookies, DeviceID, ChatGPTAccountID, etc
	Metadata         map[string]string `json:"metadata,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}
