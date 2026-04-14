package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNoEligibleAccounts = errors.New("no eligible accounts")
	ErrLeaseNotFound      = errors.New("lease not found")
	ErrPolicyRateLimit    = errors.New("rate limit policy signal")
	ErrAuthFailure        = errors.New("authentication or authorization failure")
	ErrTransientUpstream  = errors.New("transient upstream failure")
)

type AccountRoutingStatus string

const (
	AccountRoutingActive            AccountRoutingStatus = "active"
	AccountRoutingCooldown          AccountRoutingStatus = "cooldown"
	AccountRoutingDisabled          AccountRoutingStatus = "disabled"
	AccountRoutingAttentionRequired AccountRoutingStatus = "attention_required"
)

type AccountState struct {
	AccountID       string
	Provider        string
	Status          AccountRoutingStatus
	LastUsedAt      *time.Time
	DailyUsageCount int
	DailyLimit      int
	UsageDate       string
	CooldownUntil   *time.Time
	LatencyEWMAMs   float64
	ErrorCount      int
	PlanPriority    int
	ActiveLeases    int
	MaxConcurrent   int
	RetryAfterUntil *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type AccountStateManager interface {
	UpsertAccountState(ctx context.Context, state AccountState) error
	GetAccountState(ctx context.Context, accountID string) (*AccountState, error)
	RecordSuccess(ctx context.Context, accountID string, latencyMs float64) error
	RecordRateLimit(ctx context.Context, accountID string, retryAfterSeconds int) error
	RecordAuthFailure(ctx context.Context, accountID string) error
	RecordTransientFailure(ctx context.Context, accountID string) error
	ReleaseLease(ctx context.Context, lease Lease) error
}
