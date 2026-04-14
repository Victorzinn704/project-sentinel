package domain

import "time"

// ProviderResult carries the outcome of an upstream provider call back to
// the HTTP layer. It is populated even on error paths so the caller can
// decide cooldown behavior (RetryAfter is the upstream-signaled retry
// window, zero when not provided).
type ProviderResult struct {
	RequestID  string
	ResourceID string
	StatusCode int
	RetryAfter time.Duration
}
