package domain

import (
	"errors"
	"time"
)

const CurrentSessionVersion = 3

var ErrNotFound = errors.New("not found")

// Session represents an authenticated ChatGPT web session.
// Each session corresponds to a ChatGPT Plus account whose credentials
// (access token, raw cookies, user-agent) were extracted manually from a
// real browser session.
type Session struct {
	AccountID        string            `json:"account_id"`
	Provider         string            `json:"provider"`                    // Multi-provider identifier
	AccessToken      string            `json:"access_token"`                // Bearer token for Authorization header
	UserAgent        string            `json:"user_agent"`                  // Exact browser UA string used during extraction
	AuthParams       map[string]string `json:"auth_params,omitempty"`       // Replaces RawCookies, DeviceID, ChatGPTAccountID, Headers
	LastUsedAt       time.Time         `json:"last_used_at"`
	Version          int               `json:"version"`
}
