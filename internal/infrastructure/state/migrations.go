package state

const InitialSchema = `
CREATE TABLE IF NOT EXISTS accounts (
	account_id TEXT PRIMARY KEY,
	provider TEXT NOT NULL DEFAULT 'chatgpt',
	status TEXT NOT NULL CHECK (status IN ('active', 'cooldown', 'disabled', 'attention_required')),
	last_used_at TEXT,
	daily_usage_count INTEGER NOT NULL DEFAULT 0,
	daily_limit INTEGER NOT NULL DEFAULT 100,
	usage_date TEXT NOT NULL DEFAULT (date('now')),
	cooldown_until TEXT,
	latency_ewma_ms REAL NOT NULL DEFAULT 0,
	error_count INTEGER NOT NULL DEFAULT 0,
	plan_priority INTEGER NOT NULL DEFAULT 0,
	active_leases INTEGER NOT NULL DEFAULT 0,
	max_concurrent INTEGER NOT NULL DEFAULT 1,
	retry_after_until TEXT,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_accounts_routing
ON accounts (
	status,
	daily_usage_count,
	last_used_at,
	latency_ewma_ms,
	plan_priority
);

CREATE TABLE IF NOT EXISTS account_leases (
	lease_id TEXT PRIMARY KEY,
	account_id TEXT NOT NULL,
	request_id TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	acquired_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	released_at TEXT,
	FOREIGN KEY (account_id) REFERENCES accounts(account_id)
);

`
