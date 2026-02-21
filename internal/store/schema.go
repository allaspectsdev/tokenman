package store

// SQL schema constants for all TokenMan tables.

const schemaRequests = `
CREATE TABLE IF NOT EXISTS requests (
    id TEXT PRIMARY KEY,
    timestamp TEXT NOT NULL,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    format TEXT NOT NULL,
    model TEXT NOT NULL,
    tokens_in INTEGER NOT NULL DEFAULT 0,
    tokens_out INTEGER NOT NULL DEFAULT 0,
    tokens_cached INTEGER NOT NULL DEFAULT 0,
    tokens_saved INTEGER NOT NULL DEFAULT 0,
    cost_usd REAL NOT NULL DEFAULT 0.0,
    savings_usd REAL NOT NULL DEFAULT 0.0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    status_code INTEGER NOT NULL DEFAULT 0,
    cache_hit INTEGER NOT NULL DEFAULT 0,
    request_type TEXT NOT NULL DEFAULT 'normal',
    provider TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_requests_timestamp ON requests(timestamp);
CREATE INDEX IF NOT EXISTS idx_requests_model ON requests(model);
`

const schemaCache = `
CREATE TABLE IF NOT EXISTS cache (
    key TEXT PRIMARY KEY,
    model TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response_body BLOB NOT NULL,
    tokens_saved INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    hit_count INTEGER NOT NULL DEFAULT 0,
    last_hit TEXT
);
CREATE INDEX IF NOT EXISTS idx_cache_expires ON cache(expires_at);
`

const schemaFingerprints = `
CREATE TABLE IF NOT EXISTS fingerprints (
    hash TEXT PRIMARY KEY,
    content_type TEXT NOT NULL,
    token_count INTEGER NOT NULL DEFAULT 0,
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    hit_count INTEGER NOT NULL DEFAULT 1
);
`

const schemaBudgets = `
CREATE TABLE IF NOT EXISTS budgets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    period TEXT NOT NULL,
    period_start TEXT NOT NULL,
    amount_usd REAL NOT NULL DEFAULT 0.0,
    limit_usd REAL NOT NULL DEFAULT 0.0,
    last_updated TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_budgets_period ON budgets(period, period_start);
`

const schemaPIILog = `
CREATE TABLE IF NOT EXISTS pii_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    pii_type TEXT NOT NULL,
    action TEXT NOT NULL,
    field_path TEXT NOT NULL DEFAULT '',
    context TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_pii_request ON pii_log(request_id);
CREATE INDEX IF NOT EXISTS idx_pii_timestamp ON pii_log(timestamp);
`

const schemaMigrations = `
CREATE TABLE IF NOT EXISTS migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);
`

// allSchemas is the ordered list of schema DDL statements that form
// the initial (version-1) database layout.
var allSchemas = []string{
	schemaRequests,
	schemaCache,
	schemaFingerprints,
	schemaBudgets,
	schemaPIILog,
	schemaMigrations,
}
