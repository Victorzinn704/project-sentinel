package state

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
	_ "modernc.org/sqlite"
)

func TestMigrateUpgradesLegacySchemaAndSupportsLeaseLifecycle(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir() + "/state.db"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = db.Exec(`
CREATE TABLE accounts (
	account_id TEXT PRIMARY KEY,
	status TEXT NOT NULL CHECK (status IN ('active', 'cooldown', 'disabled', 'attention_required')),
	last_used_at TEXT,
	daily_usage_count INTEGER NOT NULL DEFAULT 0,
	daily_limit INTEGER NOT NULL,
	cooldown_until TEXT,
	latency_ewma_ms REAL,
	error_count INTEGER NOT NULL DEFAULT 0,
	plan_priority INTEGER NOT NULL DEFAULT 0,
	active_leases INTEGER NOT NULL DEFAULT 0,
	max_concurrent INTEGER NOT NULL DEFAULT 1,
	retry_after_until TEXT
);
CREATE TABLE account_leases (
	lease_id TEXT PRIMARY KEY,
	account_id TEXT NOT NULL,
	request_id TEXT NOT NULL,
	acquired_at TEXT NOT NULL,
	released_at TEXT
);
`)
	if err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := OpenSQLiteStateStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}

	if err := store.UpsertAccountState(ctx, domain.AccountState{
		AccountID:     "acc_test",
		Provider:      domain.ProviderChatGPT,
		Status:        domain.AccountRoutingActive,
		DailyLimit:    10,
		MaxConcurrent: 1,
	}); err != nil {
		t.Fatalf("upsert account state: %v", err)
	}

	lease, selected, err := store.AcquireBestAccountLease(ctx, "req_test")
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if selected.AccountID != "acc_test" {
		t.Fatalf("selected account = %q, want acc_test", selected.AccountID)
	}
	if lease.ResourceID != "acc_test" {
		t.Fatalf("lease resource = %q, want acc_test", lease.ResourceID)
	}

	if err := store.ReleaseLease(ctx, *lease); err != nil {
		t.Fatalf("release lease: %v", err)
	}
}

func TestAcquireLeaseRespectsProviderAndGlobalForceMode(t *testing.T) {
	t.Parallel()

	store, err := OpenSQLiteStateStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	for _, state := range []domain.AccountState{
		{
			AccountID:     "acc_chatgpt",
			Provider:      domain.ProviderChatGPT,
			Status:        domain.AccountRoutingActive,
			DailyLimit:    10,
			MaxConcurrent: 1,
		},
		{
			AccountID:     "acc_claude",
			Provider:      domain.ProviderClaude,
			Status:        domain.AccountRoutingActive,
			DailyLimit:    10,
			MaxConcurrent: 1,
		},
	} {
		if err := store.UpsertAccountState(ctx, state); err != nil {
			t.Fatalf("upsert account state %s: %v", state.AccountID, err)
		}
	}

	lease, selected, err := store.AcquireLease(ctx, domain.AccountLeaseRequest{
		RequestID: "req_chatgpt",
		Provider:  domain.ProviderChatGPT,
	})
	if err != nil {
		t.Fatalf("acquire chatgpt lease: %v", err)
	}
	if selected.AccountID != "acc_chatgpt" {
		t.Fatalf("selected account = %q, want acc_chatgpt", selected.AccountID)
	}
	if err := store.ReleaseLease(ctx, *lease); err != nil {
		t.Fatalf("release chatgpt lease: %v", err)
	}

	if err := store.SetForceMode(ctx, "acc_claude"); err != nil {
		t.Fatalf("set force mode: %v", err)
	}

	forceState, err := store.GetForceModeState(ctx)
	if err != nil {
		t.Fatalf("get force mode state: %v", err)
	}
	if !forceState.Active || forceState.AccountID != "acc_claude" {
		t.Fatalf("force state = %+v, want active acc_claude", forceState)
	}

	lease, selected, err = store.AcquireLease(ctx, domain.AccountLeaseRequest{
		RequestID: "req_forced",
		Provider:  domain.ProviderClaude,
	})
	if err != nil {
		t.Fatalf("acquire forced claude lease: %v", err)
	}
	if selected.AccountID != "acc_claude" {
		t.Fatalf("selected forced account = %q, want acc_claude", selected.AccountID)
	}
	if err := store.ReleaseLease(ctx, *lease); err != nil {
		t.Fatalf("release forced lease: %v", err)
	}

	if _, _, err := store.AcquireLease(ctx, domain.AccountLeaseRequest{
		RequestID: "req_force_mismatch",
		Provider:  domain.ProviderGemini,
	}); !errors.Is(err, domain.ErrNoEligibleAccounts) {
		t.Fatalf("force mismatch error = %v, want %v", err, domain.ErrNoEligibleAccounts)
	}
}

func TestSetAccountRoutingStatusClearsForceModeForDisabledAccount(t *testing.T) {
	t.Parallel()

	store, err := OpenSQLiteStateStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := store.UpsertAccountState(ctx, domain.AccountState{
		AccountID:     "acc_test",
		Provider:      domain.ProviderChatGPT,
		Status:        domain.AccountRoutingActive,
		DailyLimit:    10,
		MaxConcurrent: 1,
	}); err != nil {
		t.Fatalf("upsert account state: %v", err)
	}
	if err := store.SetForceMode(ctx, "acc_test"); err != nil {
		t.Fatalf("set force mode: %v", err)
	}

	if err := store.SetAccountRoutingStatus(ctx, "acc_test", domain.AccountRoutingDisabled); err != nil {
		t.Fatalf("disable account: %v", err)
	}

	forceState, err := store.GetForceModeState(ctx)
	if err != nil {
		t.Fatalf("get force mode state: %v", err)
	}
	if forceState.Active {
		t.Fatalf("force mode should be cleared after disabling account, got %+v", forceState)
	}
}

func TestAcquireLeasePrefersQuotaSnapshotWhenQuotaFirst(t *testing.T) {
	t.Parallel()

	store, err := OpenSQLiteStateStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	store.SetRotationStrategy(domain.RotationQuotaFirst)

	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertAccountState(ctx, domain.AccountState{
		AccountID:            "acc_low",
		Provider:             domain.ProviderChatGPT,
		Status:               domain.AccountRoutingActive,
		DailyLimit:           10,
		MaxConcurrent:        1,
		QuotaSource:          "chatgpt_wham_usage",
		QuotaRefreshedAt:     &now,
		FiveHourRemainingPct: intPtr(12),
		WeeklyRemainingPct:   intPtr(60),
	}); err != nil {
		t.Fatalf("upsert low quota account: %v", err)
	}
	if err := store.UpsertAccountState(ctx, domain.AccountState{
		AccountID:            "acc_high",
		Provider:             domain.ProviderChatGPT,
		Status:               domain.AccountRoutingActive,
		DailyLimit:           10,
		MaxConcurrent:        1,
		QuotaSource:          "chatgpt_wham_usage",
		QuotaRefreshedAt:     &now,
		FiveHourRemainingPct: intPtr(80),
		WeeklyRemainingPct:   intPtr(70),
	}); err != nil {
		t.Fatalf("upsert high quota account: %v", err)
	}

	lease, selected, err := store.AcquireBestAccountLease(ctx, "req_quota_first")
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if selected.AccountID != "acc_high" {
		t.Fatalf("selected account = %q, want acc_high", selected.AccountID)
	}
	if err := store.ReleaseLease(ctx, *lease); err != nil {
		t.Fatalf("release lease: %v", err)
	}
}

func TestAcquireLeaseSkipsQuotaBlockedAccount(t *testing.T) {
	t.Parallel()

	store, err := OpenSQLiteStateStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	store.SetRotationStrategy(domain.RotationQuotaFirst)

	blockedUntil := time.Now().UTC().Add(30 * time.Minute)
	if err := store.UpsertAccountState(ctx, domain.AccountState{
		AccountID:            "acc_blocked",
		Provider:             domain.ProviderChatGPT,
		Status:               domain.AccountRoutingActive,
		DailyLimit:           10,
		MaxConcurrent:        1,
		QuotaBlockedUntil:    &blockedUntil,
		FiveHourRemainingPct: intPtr(0),
		WeeklyRemainingPct:   intPtr(0),
	}); err != nil {
		t.Fatalf("upsert blocked account: %v", err)
	}
	if err := store.UpsertAccountState(ctx, domain.AccountState{
		AccountID:            "acc_open",
		Provider:             domain.ProviderChatGPT,
		Status:               domain.AccountRoutingActive,
		DailyLimit:           10,
		MaxConcurrent:        1,
		FiveHourRemainingPct: intPtr(40),
		WeeklyRemainingPct:   intPtr(40),
	}); err != nil {
		t.Fatalf("upsert open account: %v", err)
	}

	lease, selected, err := store.AcquireBestAccountLease(ctx, "req_skip_blocked")
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if selected.AccountID != "acc_open" {
		t.Fatalf("selected account = %q, want acc_open", selected.AccountID)
	}
	if err := store.ReleaseLease(ctx, *lease); err != nil {
		t.Fatalf("release lease: %v", err)
	}
}

func intPtr(value int) *int {
	return &value
}
