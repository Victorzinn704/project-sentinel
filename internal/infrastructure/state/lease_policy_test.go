package state

import (
	"strings"
	"testing"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
)

func TestLeaseSelectionPolicySelectBestQueryAddsProviderFilter(t *testing.T) {
	policy := newLeaseSelectionPolicy(domain.ProviderClaude, time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC), domain.RotationLeastUsed)
	query, args := policy.selectBestQuery()

	if !strings.Contains(query, "AND provider = ?") {
		t.Fatalf("expected provider filter in query, got %q", query)
	}
	if len(args) != 3 || args[2] != domain.ProviderClaude {
		t.Fatalf("args = %#v, want provider as third arg", args)
	}
}

func TestLeaseSelectionPolicyAllowsPinnedAccount(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	policy := newLeaseSelectionPolicy(domain.ProviderChatGPT, now, domain.RotationLeastUsed)

	active := &domain.AccountState{
		AccountID:       "acc_ok",
		Provider:        domain.ProviderChatGPT,
		Status:          domain.AccountRoutingActive,
		DailyUsageCount: 1,
		DailyLimit:      10,
		ActiveLeases:    0,
		MaxConcurrent:   1,
	}
	if !policy.allowsPinnedAccount(active) {
		t.Fatal("expected active matching account to be eligible")
	}

	blockedUntil := now.Add(5 * time.Minute)
	blocked := &domain.AccountState{
		AccountID:         "acc_blocked",
		Provider:          domain.ProviderChatGPT,
		Status:            domain.AccountRoutingActive,
		DailyLimit:        10,
		MaxConcurrent:     1,
		QuotaBlockedUntil: &blockedUntil,
	}
	if policy.allowsPinnedAccount(blocked) {
		t.Fatal("expected quota-blocked account to be ineligible")
	}
}

func TestLeaseSelectionPolicyGatesOnRealQuotaRemaining(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	policy := newLeaseSelectionPolicy(domain.ProviderChatGPT, now, domain.RotationQuotaFirst)

	zero := 0
	fifty := 50

	// Daily counter exhausted but real quota still has headroom → eligible.
	realQuotaAvailable := &domain.AccountState{
		AccountID:            "acc_daily_saturated",
		Provider:             domain.ProviderChatGPT,
		Status:               domain.AccountRoutingActive,
		DailyUsageCount:      9999,
		DailyLimit:           100,
		MaxConcurrent:        1,
		FiveHourRemainingPct: &fifty,
		WeeklyRemainingPct:   &fifty,
	}
	if !policy.allowsPinnedAccount(realQuotaAvailable) {
		t.Fatal("account with real quota left must be eligible regardless of synthetic daily counter")
	}

	// Real 5h quota at zero → ineligible.
	fiveHourExhausted := &domain.AccountState{
		AccountID:            "acc_5h_zero",
		Provider:             domain.ProviderChatGPT,
		Status:               domain.AccountRoutingActive,
		DailyLimit:           100,
		MaxConcurrent:        1,
		FiveHourRemainingPct: &zero,
		WeeklyRemainingPct:   &fifty,
	}
	if policy.allowsPinnedAccount(fiveHourExhausted) {
		t.Fatal("account with five_hour_remaining_pct=0 must be ineligible")
	}

	weeklyExhausted := &domain.AccountState{
		AccountID:            "acc_week_zero",
		Provider:             domain.ProviderChatGPT,
		Status:               domain.AccountRoutingActive,
		DailyLimit:           100,
		MaxConcurrent:        1,
		FiveHourRemainingPct: &fifty,
		WeeklyRemainingPct:   &zero,
	}
	if policy.allowsPinnedAccount(weeklyExhausted) {
		t.Fatal("account with weekly_remaining_pct=0 must be ineligible")
	}

	// No snapshot yet (NULL pct) → eligible; upstream 429 will set quota_blocked_until.
	noSnapshot := &domain.AccountState{
		AccountID:     "acc_no_snapshot",
		Provider:      domain.ProviderChatGPT,
		Status:        domain.AccountRoutingActive,
		DailyLimit:    100,
		MaxConcurrent: 1,
	}
	if !policy.allowsPinnedAccount(noSnapshot) {
		t.Fatal("account without quota snapshot must be eligible by default")
	}
}

func TestLeaseSelectionPolicySelectBestQueryUsesRealQuotaGate(t *testing.T) {
	policy := newLeaseSelectionPolicy(domain.ProviderChatGPT, time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC), domain.RotationQuotaFirst)
	query, _ := policy.selectBestQuery()

	if strings.Contains(query, "daily_usage_count < daily_limit") {
		t.Fatalf("query still gates on synthetic daily counter:\n%s", query)
	}
	if !strings.Contains(query, "five_hour_remaining_pct IS NULL OR five_hour_remaining_pct > 0") {
		t.Fatalf("query missing five_hour quota gate:\n%s", query)
	}
	if !strings.Contains(query, "weekly_remaining_pct IS NULL OR weekly_remaining_pct > 0") {
		t.Fatalf("query missing weekly quota gate:\n%s", query)
	}
}
