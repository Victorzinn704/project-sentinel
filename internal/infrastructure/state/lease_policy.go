package state

import (
	"strings"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

type leaseSelectionPolicy struct {
	provider string
	now      time.Time
	strategy domain.RotationStrategy
}

func newLeaseSelectionPolicy(provider string, now time.Time, strategy domain.RotationStrategy) leaseSelectionPolicy {
	return leaseSelectionPolicy{
		provider: strings.TrimSpace(provider),
		now:      now,
		strategy: strategy,
	}
}

func (p leaseSelectionPolicy) selectBestQuery() (string, []any) {
	query := `
SELECT
	account_id,
	provider,
	status,
	last_used_at,
	daily_usage_count,
	daily_limit,
	usage_date,
	cooldown_until,
	latency_ewma_ms,
	error_count,
	plan_priority,
	active_leases,
	max_concurrent,
	retry_after_until,
	quota_source,
	quota_refreshed_at,
	quota_blocked_until,
	five_hour_remaining_pct,
	five_hour_reset_at,
	weekly_remaining_pct,
	weekly_reset_at,
	created_at,
	updated_at
FROM accounts
WHERE status = 'active'
  AND (cooldown_until IS NULL OR cooldown_until <= ?)
  AND (quota_blocked_until IS NULL OR quota_blocked_until <= ?)
  AND (five_hour_remaining_pct IS NULL OR five_hour_remaining_pct > 0)
  AND (weekly_remaining_pct IS NULL OR weekly_remaining_pct > 0)
  AND active_leases < max_concurrent
`

	args := []any{formatTime(p.now), formatTime(p.now)}
	if p.provider != "" {
		query += "  AND provider = ?\n"
		args = append(args, p.provider)
	}

	query += rotationOrderBy(p.strategy) + "\nLIMIT 1\n"
	return query, args
}

func (p leaseSelectionPolicy) allowsPinnedAccount(selected *domain.AccountState) bool {
	if selected == nil {
		return false
	}
	if p.provider != "" && selected.Provider != p.provider {
		return false
	}
	return accountEligible(selected, p.now)
}

// rotationOrderBy returns the SQL ORDER BY fragment that implements the strategy.
// Each clause is hand-written; interpolation is safe because the value comes
// from a closed enum, not from user input.
func rotationOrderBy(strategy domain.RotationStrategy) string {
	switch strategy {
	case domain.RotationQuotaFirst:
		return `ORDER BY
	CASE
		WHEN quota_refreshed_at IS NULL THEN 1
		ELSE 0
	END ASC,
	CASE
		WHEN five_hour_remaining_pct IS NULL AND weekly_remaining_pct IS NULL THEN -1
		WHEN five_hour_remaining_pct IS NULL THEN weekly_remaining_pct
		WHEN weekly_remaining_pct IS NULL THEN five_hour_remaining_pct
		WHEN five_hour_remaining_pct < weekly_remaining_pct THEN five_hour_remaining_pct
		ELSE weekly_remaining_pct
	END DESC,
	plan_priority DESC,
	daily_usage_count ASC,
	COALESCE(last_used_at, '1970-01-01T00:00:00Z') ASC,
	latency_ewma_ms ASC`
	case domain.RotationRoundRobin:
		return `ORDER BY COALESCE(last_used_at, '1970-01-01T00:00:00Z') ASC`
	case domain.RotationRandom:
		return `ORDER BY RANDOM()`
	case domain.RotationWeighted:
		return `ORDER BY
	plan_priority DESC,
	COALESCE(last_used_at, '1970-01-01T00:00:00Z') ASC,
	daily_usage_count ASC`
	default:
		return `ORDER BY
	daily_usage_count ASC,
	COALESCE(last_used_at, '1970-01-01T00:00:00Z') ASC,
	latency_ewma_ms ASC,
	plan_priority DESC`
	}
}

func accountEligible(selected *domain.AccountState, now time.Time) bool {
	if selected == nil {
		return false
	}
	if selected.Status != domain.AccountRoutingActive {
		return false
	}
	if selected.CooldownUntil != nil && selected.CooldownUntil.After(now) {
		return false
	}
	if selected.QuotaBlockedUntil != nil && selected.QuotaBlockedUntil.After(now) {
		return false
	}
	if selected.FiveHourRemainingPct != nil && *selected.FiveHourRemainingPct <= 0 {
		return false
	}
	if selected.WeeklyRemainingPct != nil && *selected.WeeklyRemainingPct <= 0 {
		return false
	}
	if selected.ActiveLeases >= selected.MaxConcurrent {
		return false
	}
	return true
}
