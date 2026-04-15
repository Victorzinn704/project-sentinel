package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seu-usuario/project-sentinel/internal/domain"
	_ "modernc.org/sqlite"
)

const (
	defaultDailyLimit    = 100
	defaultMaxConcurrent = 1
	defaultLeaseTTL      = 2 * time.Minute
	defaultCooldown      = time.Hour
	ewmaAlpha            = 0.2
)

type SQLiteStateStore struct {
	db               *sql.DB
	leaseTTL         time.Duration
	defaultCooldown  time.Duration
	rotationStrategy domain.RotationStrategy
}

func OpenSQLiteStateStore(path string) (*SQLiteStateStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite state store: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("set sqlite busy timeout: %w; close failed: %v", err, closeErr)
		}
		return nil, fmt.Errorf("set sqlite busy timeout: %w", err)
	}

	return &SQLiteStateStore{
		db:               db,
		leaseTTL:         defaultLeaseTTL,
		defaultCooldown:  defaultCooldown,
		rotationStrategy: domain.RotationQuotaFirst,
	}, nil
}

func (s *SQLiteStateStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStateStore) Ready(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("sqlite state store is not ready: %w", err)
	}

	return nil
}

func (s *SQLiteStateStore) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, InitialSchema); err != nil {
		return fmt.Errorf("run sqlite migrations: %w", err)
	}

	columns := []struct {
		table      string
		name       string
		definition string
	}{
		{"accounts", "provider", "TEXT NOT NULL DEFAULT 'chatgpt'"},
		{"accounts", "usage_date", "TEXT NOT NULL DEFAULT '1970-01-01'"},
		{"accounts", "created_at", "TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP"},
		{"accounts", "updated_at", "TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP"},
		{"accounts", "quota_source", "TEXT NOT NULL DEFAULT ''"},
		{"accounts", "quota_refreshed_at", "TEXT"},
		{"accounts", "quota_blocked_until", "TEXT"},
		{"accounts", "five_hour_remaining_pct", "INTEGER"},
		{"accounts", "five_hour_reset_at", "TEXT"},
		{"accounts", "weekly_remaining_pct", "INTEGER"},
		{"accounts", "weekly_reset_at", "TEXT"},
		{"account_leases", "expires_at", "TEXT NOT NULL DEFAULT '1970-01-01T00:00:00Z'"},
		{"account_leases", "created_at", "TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP"},
		{"account_leases", "acquired_at", "TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP"},
	}
	for _, column := range columns {
		if err := s.ensureColumn(ctx, column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS idx_account_leases_active
ON account_leases (account_id, released_at, expires_at);
`); err != nil {
		return fmt.Errorf("create account lease index: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS idx_accounts_provider_routing
ON accounts (
	provider,
	status,
	quota_blocked_until,
	five_hour_remaining_pct,
	weekly_remaining_pct,
	daily_usage_count,
	last_used_at,
	latency_ewma_ms,
	plan_priority
);
`); err != nil {
		return fmt.Errorf("create provider routing index: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS routing_settings (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	forced_account_id TEXT,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT OR IGNORE INTO routing_settings (id, forced_account_id, updated_at)
VALUES (1, NULL, CURRENT_TIMESTAMP);
`); err != nil {
		return fmt.Errorf("create routing settings table: %w", err)
	}

	return nil
}

func (s *SQLiteStateStore) ensureColumn(ctx context.Context, table string, column string, definition string) error {
	exists, err := s.columnExists(ctx, table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)
	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("add sqlite column %s.%s: %w", table, column, err)
	}

	return nil
}

func (s *SQLiteStateStore) columnExists(ctx context.Context, table string, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, fmt.Errorf("inspect sqlite table %s: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return false, fmt.Errorf("scan sqlite table %s info: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate sqlite table %s info: %w", table, err)
	}

	return false, nil
}

func (s *SQLiteStateStore) UpsertAccountState(ctx context.Context, state domain.AccountState) error {
	now := time.Now().UTC()
	state = withStateDefaults(state, now)

	_, err := s.db.ExecContext(ctx, `
INSERT INTO accounts (
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
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id) DO UPDATE SET
	provider = excluded.provider,
	daily_limit = excluded.daily_limit,
	plan_priority = excluded.plan_priority,
	max_concurrent = MAX(excluded.max_concurrent, accounts.max_concurrent),
	quota_source = excluded.quota_source,
	quota_refreshed_at = excluded.quota_refreshed_at,
	quota_blocked_until = excluded.quota_blocked_until,
	five_hour_remaining_pct = excluded.five_hour_remaining_pct,
	five_hour_reset_at = excluded.five_hour_reset_at,
	weekly_remaining_pct = excluded.weekly_remaining_pct,
	weekly_reset_at = excluded.weekly_reset_at,
	updated_at = excluded.updated_at
`,
		state.AccountID,
		state.Provider,
		string(state.Status),
		formatNullableTime(state.LastUsedAt),
		state.DailyUsageCount,
		state.DailyLimit,
		state.UsageDate,
		formatNullableTime(state.CooldownUntil),
		state.LatencyEWMAMs,
		state.ErrorCount,
		state.PlanPriority,
		state.ActiveLeases,
		state.MaxConcurrent,
		formatNullableTime(state.RetryAfterUntil),
		state.QuotaSource,
		formatNullableTime(state.QuotaRefreshedAt),
		formatNullableTime(state.QuotaBlockedUntil),
		nullableInt(state.FiveHourRemainingPct),
		formatNullableTime(state.FiveHourResetAt),
		nullableInt(state.WeeklyRemainingPct),
		formatNullableTime(state.WeeklyResetAt),
		formatTime(now),
		formatTime(now),
	)
	if err != nil {
		return fmt.Errorf("upsert account state: %w", err)
	}

	return nil
}

func (s *SQLiteStateStore) GetAccountState(ctx context.Context, accountID string) (*domain.AccountState, error) {
	row := s.db.QueryRowContext(ctx, `
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
WHERE account_id = ?
`, accountID)

	state, err := scanAccountState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return state, nil
}

// SetRotationStrategy replaces the strategy used by future AcquireBestAccountLease
// calls. The store starts with RotationLeastUsed; main.go overrides from config.
func (s *SQLiteStateStore) SetRotationStrategy(strategy domain.RotationStrategy) {
	s.rotationStrategy = strategy
}

// RotationStrategy returns the currently active rotation strategy.
func (s *SQLiteStateStore) RotationStrategy() domain.RotationStrategy {
	return s.rotationStrategy
}

func (s *SQLiteStateStore) AcquireBestAccountLease(ctx context.Context, requestID string) (*domain.Lease, *domain.AccountState, error) {
	return s.AcquireLease(ctx, domain.AccountLeaseRequest{
		RequestID: requestID,
	})
}

// AcquireAccountLease pins a lease to a specific account, bypassing rotation.
// Returns ErrNotFound if the account doesn't exist, ErrNoEligibleAccounts if
// it's disabled, in cooldown, over quota, or already at max concurrency.
// Used by the X-Sentinel-Force-Account header path.
func (s *SQLiteStateStore) AcquireAccountLease(ctx context.Context, accountID string, requestID string) (*domain.Lease, *domain.AccountState, error) {
	return s.AcquireLease(ctx, domain.AccountLeaseRequest{
		RequestID:       requestID,
		ForcedAccountID: accountID,
	})
}

func (s *SQLiteStateStore) AcquireLease(ctx context.Context, req domain.AccountLeaseRequest) (*domain.Lease, *domain.AccountState, error) {
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = "request_" + randomHex(8)
	}

	provider := strings.TrimSpace(req.Provider)
	if provider != "" {
		normalizedProvider, err := domain.NormalizeProvider(provider)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", domain.ErrInvalidData, err)
		}
		provider = normalizedProvider
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire sqlite connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, nil, fmt.Errorf("begin immediate transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
			if rollbackErr != nil {
				// Preserve the original transaction error.
			}
		}
	}()

	now := time.Now().UTC()
	if err := s.refreshEligibility(ctx, conn, now); err != nil {
		return nil, nil, err
	}
	policy := newLeaseSelectionPolicy(provider, now, s.rotationStrategy)

	forcedAccountID := strings.TrimSpace(req.ForcedAccountID)
	if forcedAccountID == "" {
		forcedAccountID, err = s.currentForcedAccountIDTx(ctx, conn)
		if err != nil {
			return nil, nil, err
		}
	}

	var selected *domain.AccountState
	if forcedAccountID != "" {
		selected, err = s.selectPinnedAccountTx(ctx, conn, forcedAccountID, policy)
	} else {
		selected, err = s.selectBestAccountTx(ctx, conn, policy)
	}
	if err != nil {
		return nil, nil, err
	}

	lease, err := s.insertLeaseTx(ctx, conn, selected, requestID, now)
	if err != nil {
		return nil, nil, err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, nil, fmt.Errorf("commit account lease transaction: %w", err)
	}
	committed = true

	selected.ActiveLeases++
	return lease, selected, nil
}

func (s *SQLiteStateStore) selectBestAccountTx(ctx context.Context, conn *sql.Conn, policy leaseSelectionPolicy) (*domain.AccountState, error) {
	query, args := policy.selectBestQuery()
	row := conn.QueryRowContext(ctx, query, args...)
	selected, err := scanAccountState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNoEligibleAccounts
	}
	if err != nil {
		return nil, err
	}

	return selected, nil
}

func (s *SQLiteStateStore) selectPinnedAccountTx(ctx context.Context, conn *sql.Conn, accountID string, policy leaseSelectionPolicy) (*domain.AccountState, error) {
	row := conn.QueryRowContext(ctx, `
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
WHERE account_id = ?
`, accountID)

	selected, err := scanAccountState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !policy.allowsPinnedAccount(selected) {
		return nil, domain.ErrNoEligibleAccounts
	}

	return selected, nil
}

func (s *SQLiteStateStore) GetForceModeState(ctx context.Context) (domain.ForceModeState, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT forced_account_id, updated_at
FROM routing_settings
WHERE id = 1
`)

	var (
		forcedAccountID sql.NullString
		updatedAt       string
	)
	if err := row.Scan(&forcedAccountID, &updatedAt); err != nil {
		return domain.ForceModeState{}, fmt.Errorf("get force mode state: %w", err)
	}

	return domain.ForceModeState{
		Active:    forcedAccountID.Valid && strings.TrimSpace(forcedAccountID.String) != "",
		AccountID: strings.TrimSpace(forcedAccountID.String),
		UpdatedAt: parseTime(updatedAt),
	}, nil
}

func (s *SQLiteStateStore) SetForceMode(ctx context.Context, accountID string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return s.ClearForceMode(ctx)
	}

	account, err := s.GetAccountState(ctx, accountID)
	if err != nil {
		return err
	}
	if account.Status == domain.AccountRoutingDisabled {
		return domain.ErrNoEligibleAccounts
	}

	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
UPDATE routing_settings
SET forced_account_id = ?, updated_at = ?
WHERE id = 1
`, accountID, formatTime(now)); err != nil {
		return fmt.Errorf("set force mode: %w", err)
	}

	return nil
}

func (s *SQLiteStateStore) ClearForceMode(ctx context.Context) error {
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
UPDATE routing_settings
SET forced_account_id = NULL, updated_at = ?
WHERE id = 1
`, formatTime(now)); err != nil {
		return fmt.Errorf("clear force mode: %w", err)
	}

	return nil
}

func (s *SQLiteStateStore) currentForcedAccountIDTx(ctx context.Context, conn *sql.Conn) (string, error) {
	row := conn.QueryRowContext(ctx, `
SELECT forced_account_id
FROM routing_settings
WHERE id = 1
`)

	var forcedAccountID sql.NullString
	if err := row.Scan(&forcedAccountID); err != nil {
		return "", fmt.Errorf("get forced account id: %w", err)
	}

	return strings.TrimSpace(forcedAccountID.String), nil
}

// insertLeaseTx increments the account's active_leases counter and inserts
// a row into account_leases. The caller owns the surrounding transaction.
func (s *SQLiteStateStore) insertLeaseTx(ctx context.Context, conn *sql.Conn, selected *domain.AccountState, requestID string, now time.Time) (*domain.Lease, error) {
	result, err := conn.ExecContext(ctx, `
UPDATE accounts
SET active_leases = active_leases + 1,
    updated_at = ?
WHERE account_id = ?
  AND active_leases < max_concurrent
`, formatTime(now), selected.AccountID)
	if err != nil {
		return nil, fmt.Errorf("increment account lease count: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read lease update rows affected: %w", err)
	}
	if affected != 1 {
		return nil, domain.ErrNoEligibleAccounts
	}

	lease := domain.Lease{
		LeaseID:    "lease_" + randomHex(16),
		ResourceID: selected.AccountID,
		AccountID:  selected.AccountID,
		SessionID:  requestID,
		RequestID:  requestID,
		ExpiresAt:  now.Add(s.leaseTTL),
		CreatedAt:  now,
	}

	if _, err := conn.ExecContext(ctx, `
INSERT INTO account_leases (lease_id, account_id, request_id, expires_at, created_at, acquired_at)
VALUES (?, ?, ?, ?, ?, ?)
`, lease.LeaseID, lease.AccountID, lease.RequestID, formatTime(lease.ExpiresAt), formatTime(lease.CreatedAt), formatTime(lease.CreatedAt)); err != nil {
		return nil, fmt.Errorf("insert account lease: %w", err)
	}

	return &lease, nil
}

// ListAccountStates returns a snapshot of every account's routing state,
// ordered by account_id for stable output. Used by GET /admin/accounts.
func (s *SQLiteStateStore) ListAccountStates(ctx context.Context) ([]domain.AccountState, error) {
	rows, err := s.db.QueryContext(ctx, `
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
ORDER BY account_id ASC
`)
	if err != nil {
		return nil, fmt.Errorf("list account states: %w", err)
	}
	defer rows.Close()

	var states []domain.AccountState
	for rows.Next() {
		state, err := scanAccountState(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, *state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account states: %w", err)
	}

	return states, nil
}

func (s *SQLiteStateStore) RecordSuccess(ctx context.Context, accountID string, latencyMs float64) error {
	now := time.Now().UTC()
	today := now.Format(time.DateOnly)

	_, err := s.db.ExecContext(ctx, `
UPDATE accounts
SET daily_usage_count = CASE
		WHEN usage_date < ? THEN 1
		ELSE daily_usage_count + 1
	END,
	usage_date = ?,
	last_used_at = ?,
	latency_ewma_ms = CASE
		WHEN latency_ewma_ms <= 0 THEN ?
		ELSE (latency_ewma_ms * ?) + (? * ?)
	END,
	error_count = 0,
	status = 'active',
	cooldown_until = NULL,
	retry_after_until = NULL,
	updated_at = ?
WHERE account_id = ?
`, today, today, formatTime(now), latencyMs, 1-ewmaAlpha, latencyMs, ewmaAlpha, formatTime(now), accountID)
	if err != nil {
		return fmt.Errorf("record account success: %w", err)
	}

	return nil
}

func (s *SQLiteStateStore) RecordQuotaSnapshot(ctx context.Context, accountID string, snapshot domain.AccountQuotaSnapshot) error {
	now := time.Now().UTC()
	refreshedAt := snapshot.RefreshedAt.UTC()
	if refreshedAt.IsZero() {
		refreshedAt = now
	}

	_, err := s.db.ExecContext(ctx, `
UPDATE accounts
SET quota_source = ?,
    quota_refreshed_at = ?,
    quota_blocked_until = ?,
    five_hour_remaining_pct = ?,
    five_hour_reset_at = ?,
    weekly_remaining_pct = ?,
    weekly_reset_at = ?,
    updated_at = ?
WHERE account_id = ?
`,
		strings.TrimSpace(snapshot.Source),
		formatTime(refreshedAt),
		formatNullableTime(snapshot.BlockedUntil),
		nullableInt(snapshot.FiveHourRemainingPct),
		formatNullableTime(snapshot.FiveHourResetAt),
		nullableInt(snapshot.WeeklyRemainingPct),
		formatNullableTime(snapshot.WeeklyResetAt),
		formatTime(now),
		accountID,
	)
	if err != nil {
		return fmt.Errorf("record account quota snapshot: %w", err)
	}

	return nil
}

func (s *SQLiteStateStore) RecordRateLimit(ctx context.Context, accountID string, retryAfterSeconds int) error {
	if retryAfterSeconds <= 0 {
		retryAfterSeconds = int(s.defaultCooldown.Seconds())
	}

	now := time.Now().UTC()
	until := now.Add(time.Duration(retryAfterSeconds) * time.Second)

	_, err := s.db.ExecContext(ctx, `
UPDATE accounts
SET status = 'cooldown',
    cooldown_until = ?,
    retry_after_until = ?,
    error_count = error_count + 1,
    updated_at = ?
WHERE account_id = ?
`, formatTime(until), formatTime(until), formatTime(now), accountID)
	if err != nil {
		return fmt.Errorf("record account rate limit: %w", err)
	}

	return nil
}

func (s *SQLiteStateStore) RecordAuthFailure(ctx context.Context, accountID string) error {
	now := time.Now().UTC()

	_, err := s.db.ExecContext(ctx, `
UPDATE accounts
SET status = 'attention_required',
    error_count = error_count + 1,
    updated_at = ?
WHERE account_id = ?
`, formatTime(now), accountID)
	if err != nil {
		return fmt.Errorf("record account auth failure: %w", err)
	}

	return nil
}

func (s *SQLiteStateStore) RecordTransientFailure(ctx context.Context, accountID string) error {
	now := time.Now().UTC()

	_, err := s.db.ExecContext(ctx, `
UPDATE accounts
SET error_count = error_count + 1,
    updated_at = ?
WHERE account_id = ?
`, formatTime(now), accountID)
	if err != nil {
		return fmt.Errorf("record transient account failure: %w", err)
	}

	return nil
}

func (s *SQLiteStateStore) SetAccountRoutingStatus(ctx context.Context, accountID string, status domain.AccountRoutingStatus) error {
	now := time.Now().UTC()

	result, err := s.db.ExecContext(ctx, `
UPDATE accounts
SET status = ?,
    updated_at = ?
WHERE account_id = ?
`, string(status), formatTime(now), accountID)
	if err != nil {
		return fmt.Errorf("set account routing status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read account routing status rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrNotFound
	}
	if status == domain.AccountRoutingDisabled {
		if _, err := s.db.ExecContext(ctx, `
UPDATE routing_settings
SET forced_account_id = CASE
		WHEN forced_account_id = ? THEN NULL
		ELSE forced_account_id
	END,
	updated_at = ?
WHERE id = 1
`, accountID, formatTime(now)); err != nil {
			return fmt.Errorf("clear force mode for disabled account: %w", err)
		}
	}

	return nil
}

func (s *SQLiteStateStore) ReleaseLease(ctx context.Context, lease domain.Lease) error {
	now := time.Now().UTC()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire sqlite connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin release lease transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
			if rollbackErr != nil {
				// Preserve the original transaction error.
			}
		}
	}()

	result, err := conn.ExecContext(ctx, `
UPDATE account_leases
SET released_at = ?
WHERE lease_id = ?
  AND released_at IS NULL
`, formatTime(now), lease.LeaseID)
	if err != nil {
		return fmt.Errorf("mark account lease released: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read release lease rows affected: %w", err)
	}
	if affected == 0 {
		return domain.ErrLeaseNotFound
	}

	if _, err := conn.ExecContext(ctx, `
UPDATE accounts
SET active_leases = CASE
		WHEN active_leases > 0 THEN active_leases - 1
		ELSE 0
	END,
	updated_at = ?
WHERE account_id = ?
`, formatTime(now), lease.AccountID); err != nil {
		return fmt.Errorf("decrement account lease count: %w", err)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit release lease transaction: %w", err)
	}
	committed = true

	return nil
}

// ReclaimStaleLeases closes any open account_leases and zeroes the per-account
// counters. Intended to run once at process startup: by definition no request
// can be in-flight before the server accepts connections, so every open lease
// is an orphan from a prior crash or non-graceful shutdown.
func (s *SQLiteStateStore) ReclaimStaleLeases(ctx context.Context) (int64, error) {
	now := time.Now().UTC()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire sqlite connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, fmt.Errorf("begin reclaim stale leases transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	result, err := conn.ExecContext(ctx, `
UPDATE account_leases
SET released_at = ?
WHERE released_at IS NULL
`, formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("mark stale leases released: %w", err)
	}
	reclaimed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read reclaimed leases rows affected: %w", err)
	}

	if _, err := conn.ExecContext(ctx, `
UPDATE accounts
SET active_leases = 0,
    updated_at = ?
WHERE active_leases > 0
`, formatTime(now)); err != nil {
		return 0, fmt.Errorf("reset stale active lease counters: %w", err)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return 0, fmt.Errorf("commit reclaim stale leases transaction: %w", err)
	}
	committed = true

	return reclaimed, nil
}

func (s *SQLiteStateStore) refreshEligibility(ctx context.Context, exec sqlExecutor, now time.Time) error {
	today := now.Format(time.DateOnly)
	if _, err := exec.ExecContext(ctx, `
UPDATE accounts
SET status = 'active',
    cooldown_until = NULL,
    retry_after_until = NULL,
    quota_blocked_until = CASE
		WHEN quota_blocked_until IS NOT NULL AND quota_blocked_until <= ? THEN NULL
		ELSE quota_blocked_until
	END,
    updated_at = ?
WHERE status = 'cooldown'
  AND cooldown_until IS NOT NULL
  AND cooldown_until <= ?
`, formatTime(now), formatTime(now), formatTime(now)); err != nil {
		return fmt.Errorf("expire account cooldowns: %w", err)
	}

	if _, err := exec.ExecContext(ctx, `
UPDATE accounts
SET quota_blocked_until = NULL,
    updated_at = ?
WHERE quota_blocked_until IS NOT NULL
  AND quota_blocked_until <= ?
`, formatTime(now), formatTime(now)); err != nil {
		return fmt.Errorf("expire account quota blocks: %w", err)
	}

	if _, err := exec.ExecContext(ctx, `
UPDATE accounts
SET daily_usage_count = 0,
    usage_date = ?,
    updated_at = ?
WHERE usage_date < ?
`, today, formatTime(now), today); err != nil {
		return fmt.Errorf("reset daily account usage: %w", err)
	}

	rows, err := exec.QueryContext(ctx, `
SELECT account_id, COUNT(*)
FROM account_leases
WHERE released_at IS NULL
  AND expires_at <= ?
GROUP BY account_id
`, formatTime(now))
	if err != nil {
		return fmt.Errorf("select expired account leases: %w", err)
	}
	defer rows.Close()

	type expiredLeaseCount struct {
		accountID string
		count     int
	}
	var expired []expiredLeaseCount
	for rows.Next() {
		var row expiredLeaseCount
		if err := rows.Scan(&row.accountID, &row.count); err != nil {
			return fmt.Errorf("scan expired account lease: %w", err)
		}
		expired = append(expired, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate expired account leases: %w", err)
	}

	for _, row := range expired {
		if _, err := exec.ExecContext(ctx, `
UPDATE accounts
SET active_leases = CASE
		WHEN active_leases >= ? THEN active_leases - ?
		ELSE 0
	END,
	updated_at = ?
WHERE account_id = ?
`, row.count, row.count, formatTime(now), row.accountID); err != nil {
			return fmt.Errorf("decrement expired account lease count: %w", err)
		}
	}

	if _, err := exec.ExecContext(ctx, `
UPDATE account_leases
SET released_at = ?
WHERE released_at IS NULL
  AND expires_at <= ?
`, formatTime(now), formatTime(now)); err != nil {
		return fmt.Errorf("mark expired account leases released: %w", err)
	}

	return nil
}

type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type accountStateScanner interface {
	Scan(dest ...any) error
}

func scanAccountState(row accountStateScanner) (*domain.AccountState, error) {
	var (
		state             domain.AccountState
		provider          string
		status            string
		lastUsedAt        sql.NullString
		cooldownUntil     sql.NullString
		retryAfterUntil   sql.NullString
		quotaRefreshedAt  sql.NullString
		quotaBlockedUntil sql.NullString
		fiveHourRemaining sql.NullInt64
		fiveHourResetAt   sql.NullString
		weeklyRemaining   sql.NullInt64
		weeklyResetAt     sql.NullString
		createdAt         string
		updatedAt         string
	)

	err := row.Scan(
		&state.AccountID,
		&provider,
		&status,
		&lastUsedAt,
		&state.DailyUsageCount,
		&state.DailyLimit,
		&state.UsageDate,
		&cooldownUntil,
		&state.LatencyEWMAMs,
		&state.ErrorCount,
		&state.PlanPriority,
		&state.ActiveLeases,
		&state.MaxConcurrent,
		&retryAfterUntil,
		&state.QuotaSource,
		&quotaRefreshedAt,
		&quotaBlockedUntil,
		&fiveHourRemaining,
		&fiveHourResetAt,
		&weeklyRemaining,
		&weeklyResetAt,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}

	state.Provider = provider
	state.Status = domain.AccountRoutingStatus(status)
	state.LastUsedAt = parseNullableTime(lastUsedAt)
	state.CooldownUntil = parseNullableTime(cooldownUntil)
	state.RetryAfterUntil = parseNullableTime(retryAfterUntil)
	state.QuotaRefreshedAt = parseNullableTime(quotaRefreshedAt)
	state.QuotaBlockedUntil = parseNullableTime(quotaBlockedUntil)
	state.FiveHourRemainingPct = parseNullableInt(fiveHourRemaining)
	state.FiveHourResetAt = parseNullableTime(fiveHourResetAt)
	state.WeeklyRemainingPct = parseNullableInt(weeklyRemaining)
	state.WeeklyResetAt = parseNullableTime(weeklyResetAt)
	state.CreatedAt = parseTime(createdAt)
	state.UpdatedAt = parseTime(updatedAt)

	return &state, nil
}

func withStateDefaults(state domain.AccountState, now time.Time) domain.AccountState {
	if provider, err := domain.NormalizeProvider(state.Provider); err == nil {
		state.Provider = provider
	} else {
		state.Provider = domain.ProviderChatGPT
	}
	if state.Status == "" {
		state.Status = domain.AccountRoutingActive
	}
	if state.DailyLimit <= 0 {
		state.DailyLimit = defaultDailyLimit
	}
	if state.MaxConcurrent <= 0 {
		state.MaxConcurrent = defaultMaxConcurrent
	}
	if state.UsageDate == "" {
		state.UsageDate = now.Format(time.DateOnly)
	}

	return state
}

func formatNullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}

	return formatTime(*value)
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseNullableTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}

	parsed := parseTime(value.String)
	return &parsed
}

func parseNullableInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	parsed := int(value.Int64)
	return &parsed
}

func parseTime(value string) time.Time {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC()
		}
	}

	return time.Time{}
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}

	return hex.EncodeToString(buf)
}
