-- =============================================================================
-- Nexora Cassandra Schema
-- -----------------------------------------------------------------------------
-- IMPORTANT: every table, column, primary key and index below is derived from
-- the CQL statements executed by the Go services (services/*/internal/repository).
-- If you change a repository query, run the schema contract test:
--     go test ./tests/contract/...
-- which fails when repositories reference tables/columns missing here.
--
-- Schema is idempotent (IF NOT EXISTS) so `make init-db` can be re-run safely.
-- =============================================================================

CREATE KEYSPACE IF NOT EXISTS nexora
  WITH replication = {
    'class': 'SimpleStrategy',
    'replication_factor': 1
  }
  AND durable_writes = true;

USE nexora;

-- ── Users ────────────────────────────────────────────────────────────────────
-- Keyed by user_id. Lookup tables below index email/phone writes.
CREATE TABLE IF NOT EXISTS users (
  user_id UUID PRIMARY KEY,
  email TEXT,
  phone TEXT,
  password_hash TEXT,
  first_name TEXT,
  last_name TEXT,
  status TEXT,
  email_verified BOOLEAN,
  phone_verified BOOLEAN,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users_by_email (
  email TEXT PRIMARY KEY,
  user_id UUID,
  created_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users_by_phone (
  phone TEXT PRIMARY KEY,
  user_id UUID,
  created_at TIMESTAMP
);

-- ── OTPs ─────────────────────────────────────────────────────────────────────
-- One partition per (user, purpose); newest code first via DESC clustering so
-- "LIMIT 1" always returns the most recent OTP for verification.
CREATE TABLE IF NOT EXISTS otps (
  user_id UUID,
  purpose TEXT,
  created_at TIMESTAMP,
  code TEXT,
  expires_at TIMESTAMP,
  used BOOLEAN,
  attempts INT,
  PRIMARY KEY (user_id, purpose, created_at)
) WITH CLUSTERING ORDER BY (purpose ASC, created_at DESC);

-- ── Refresh tokens (revocable, rotating) ────────────────────────────────────
-- Opaque refresh tokens are stored as SHA-256 digests so a database read does
-- not leak usable credentials. Refresh rotates: the presented token is revoked
-- and a fresh one issued, so a stolen token dies on first use or on logout.
CREATE TABLE IF NOT EXISTS refresh_tokens (
  token_hash TEXT PRIMARY KEY,
  user_id UUID,
  device_id TEXT,
  expires_at TIMESTAMP,
  revoked BOOLEAN,
  created_at TIMESTAMP
);

-- ── Devices ──────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS devices (
  user_id UUID,
  device_id UUID,
  device_name TEXT,
  device_type TEXT,
  fcm_token TEXT,
  last_active TIMESTAMP,
  created_at TIMESTAMP,
  PRIMARY KEY (user_id, device_id)
);

-- ── Accounts ─────────────────────────────────────────────────────────────────
-- Single source of account state. Balances are held as minor units (int64).
CREATE TABLE IF NOT EXISTS accounts (
  account_id UUID PRIMARY KEY,
  user_id UUID,
  account_type TEXT,
  currency TEXT,
  available_balance BIGINT,
  current_balance BIGINT,
  reserved_balance BIGINT,
  status TEXT,
  lockdown_enabled BOOLEAN,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

-- ── Ledger ───────────────────────────────────────────────────────────────────
-- Ledger transactions: the idempotent record of a double-entry transaction.
-- Queried by idempotency key (secondary index) for safe retries.
CREATE TABLE IF NOT EXISTS ledger_transactions (
  transaction_id UUID PRIMARY KEY,
  idempotency_key TEXT,
  transaction_type TEXT,
  status TEXT,
  total_amount BIGINT,
  currency TEXT,
  description TEXT,
  correlation_id TEXT,
  causation_id TEXT,
  event_version INT,
  created_at TIMESTAMP,
  completed_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ledger_tx_idempotency
  ON ledger_transactions (idempotency_key);

-- Ledger entries: append-only, partitioned by account, newest first.
-- balance_before / balance_after make each row self-verifying and allow
-- balance reconstruction purely by replaying this table.
-- category is the user-facing spend category (GROCERIES, EATING_OUT, ...)
-- inferred from the description at booking time; note is the user's own
-- annotation stored separately in transaction_notes (ledger stays immutable).
CREATE TABLE IF NOT EXISTS ledger_entries (
  account_id UUID,
  created_at TIMESTAMP,
  entry_id UUID,
  transaction_id UUID,
  entry_type TEXT,
  entry_direction TEXT,
  amount BIGINT,
  currency TEXT,
  balance_before BIGINT,
  balance_after BIGINT,
  description TEXT,
  category TEXT,
  correlation_id TEXT,
  causation_id TEXT,
  event_version INT,
  PRIMARY KEY (account_id, created_at, entry_id)
) WITH CLUSTERING ORDER BY (created_at DESC, entry_id ASC);

CREATE INDEX IF NOT EXISTS idx_ledger_entries_category
  ON ledger_entries (category);

-- User notes on transactions. Kept OUT of ledger_entries so the ledger stays
-- an append-only financial record; notes are annotations keyed by entry.
CREATE TABLE IF NOT EXISTS transaction_notes (
  entry_id UUID PRIMARY KEY,
  user_id UUID,
  note TEXT,
  updated_at TIMESTAMP
);

-- Lookups used by the ledger service:
--   GetEntriesByTransaction -> WHERE transaction_id = ?
--   SumDebits/Credits        -> WHERE account_id = ? AND entry_type = ?
CREATE INDEX IF NOT EXISTS idx_ledger_entries_transaction
  ON ledger_entries (transaction_id);
CREATE INDEX IF NOT EXISTS idx_ledger_entries_type
  ON ledger_entries (entry_type);

-- Reservations: keyed by reservation_id so release/settle can use
-- compare-and-set updates (IF status = 'ACTIVE'). Indexes back per-account
-- active queries (used with ALLOW FILTERING in the ledger repository).
CREATE TABLE IF NOT EXISTS reservations (
  reservation_id UUID PRIMARY KEY,
  account_id UUID,
  transaction_id UUID,
  amount BIGINT,
  currency TEXT,
  status TEXT,
  expires_at TIMESTAMP,
  created_at TIMESTAMP,
  released_at TIMESTAMP,
  settled_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_reservations_account
  ON reservations (account_id);
CREATE INDEX IF NOT EXISTS idx_reservations_status
  ON reservations (status);

-- ── Payments ─────────────────────────────────────────────────────────────────
-- Payment state machine (CREATED -> AUTHORIZED -> PROCESSING -> CONFIRMED ->
-- SETTLED | FAILED | UNKNOWN | REVERSED | CANCELLED).
CREATE TABLE IF NOT EXISTS payments (
  payment_id UUID PRIMARY KEY,
  idempotency_key TEXT,
  account_id UUID,
  user_id UUID,
  payment_type TEXT,
  amount BIGINT,
  currency TEXT,
  status TEXT,
  failure_reason TEXT,
  counterparty_id TEXT,
  counterparty_name TEXT,
  reference TEXT,
  ledger_transaction_id TEXT,
  reservation_id TEXT,
  metadata MAP<TEXT, TEXT>,
  fraud_score DOUBLE,
  fraud_action TEXT,
  created_at TIMESTAMP,
  updated_at TIMESTAMP,
  authorized_at TIMESTAMP,
  settled_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_payments_account
  ON payments (account_id);
CREATE INDEX IF NOT EXISTS idx_payments_idempotency
  ON payments (idempotency_key);

-- ── Transfers ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS transfers (
  transfer_id UUID PRIMARY KEY,
  idempotency_key TEXT,
  from_account_id UUID,
  to_account_id UUID,
  amount BIGINT,
  currency TEXT,
  status TEXT,
  description TEXT,
  created_at TIMESTAMP,
  completed_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_transfers_from_account
  ON transfers (from_account_id);

-- ── Cards ────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS cards (
  card_id UUID PRIMARY KEY,
  user_id UUID,
  account_id UUID,
  card_number_last4 TEXT,
  card_type TEXT,
  status TEXT,
  spending_limit BIGINT,
  daily_limit BIGINT,
  monthly_limit BIGINT,
  currency TEXT,
  online_enabled BOOLEAN,
  atm_enabled BOOLEAN,
  gambling_block_enabled BOOLEAN,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS cards_by_user (
  user_id UUID,
  card_id UUID,
  status TEXT,
  card_type TEXT,
  created_at TIMESTAMP,
  PRIMARY KEY (user_id, card_id)
);

-- Card authorizations: the real-time merchant presentment decision record.
-- Written on every tap/authorisation request with the live decision, funds
-- reservation and sub-second latency; captured/voided later by the merchant
-- clearing cycle. Listed per account/card via secondary indexes (dev scale;
-- production would use explicit lookup tables, see ADR).
CREATE TABLE IF NOT EXISTS card_authorizations (
  authorization_id UUID PRIMARY KEY,
  card_id UUID,
  user_id UUID,
  account_id UUID,
  amount BIGINT,
  currency TEXT,
  merchant TEXT,
  merchant_category TEXT,
  merchant_city TEXT,
  merchant_country TEXT,
  latitude DOUBLE,
  longitude DOUBLE,
  terminal_id TEXT,
  decision TEXT,
  decline_reason TEXT,
  risk_score DOUBLE,
  risk_action TEXT,
  risk_level TEXT,
  risk_reasons TEXT,
  reservation_id UUID,
  status TEXT,
  latency_ms BIGINT,
  created_at TIMESTAMP,
  updated_at TIMESTAMP,
  captured_at TIMESTAMP,
  voided_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_card_authz_card
  ON card_authorizations (card_id);
CREATE INDEX IF NOT EXISTS idx_card_authz_account
  ON card_authorizations (account_id);
CREATE INDEX IF NOT EXISTS idx_card_authz_user
  ON card_authorizations (user_id);

-- ── Pots (Savings Pots) ─────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS pots (
  pot_id UUID PRIMARY KEY,
  user_id UUID,
  name TEXT,
  target_amount BIGINT,
  current_amount BIGINT,
  currency TEXT,
  status TEXT,
  round_up_enabled BOOLEAN,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS pots_by_user (
  user_id UUID,
  pot_id UUID,
  name TEXT,
  status TEXT,
  created_at TIMESTAMP,
  PRIMARY KEY (user_id, pot_id)
);

-- ── Roundups ────────────────────────────────────────────────────────────────
-- Idempotency markers for the roundups consumer: one row per captured card
-- authorization that has been swept into a pot (LWT claim, exactly-once).
CREATE TABLE IF NOT EXISTS roundup_processed (
  authorization_id UUID PRIMARY KEY,
  pot_id UUID,
  amount BIGINT,
  processed_at TIMESTAMP
);

-- ── Insights (Financial Intelligence Platform) ─────────────────────────────
-- Projections maintained by insights-service from the card/payment event
-- streams. They power the app's Insights screen: subscriptions, safe-to-spend
-- and per-category spend baselines.

-- Detected recurring payments (subscriptions) per account. subscription_id is
-- a deterministic hash of the normalised merchant description so the same
-- subscription always maps to the same row across recomputes.
CREATE TABLE IF NOT EXISTS subscriptions (
  account_id UUID,
  subscription_id TEXT,
  merchant TEXT,
  monthly_amount BIGINT,
  last_amount BIGINT,
  previous_amount BIGINT,
  cadence_days INT,
  transaction_count INT,
  price_hike_pct DOUBLE,
  active BOOLEAN,
  notified BOOLEAN,
  first_seen_at TIMESTAMP,
  last_seen_at TIMESTAMP,
  next_expected_at TIMESTAMP,
  PRIMARY KEY (account_id, subscription_id)
);

-- Per-category spend baseline: daily average and 30-day total, maintained on
-- every spend event. Feeds forecast spend + category breakdown.
CREATE TABLE IF NOT EXISTS account_baselines (
  account_id UUID,
  category TEXT,
  daily_avg BIGINT,
  total_30d BIGINT,
  tx_count INT,
  updated_at TIMESTAMP,
  PRIMARY KEY (account_id, category)
);

-- Detected income (salary/payroll style credits): monthly average and the
-- most recent credit, used for payday forecasting and safe-to-spend.
CREATE TABLE IF NOT EXISTS account_income (
  account_id UUID PRIMARY KEY,
  user_id UUID,
  source TEXT,
  last_amount BIGINT,
  monthly_avg BIGINT,
  last_income_at TIMESTAMP,
  updated_at TIMESTAMP
);

-- Insight alerts (new subscription, price hike, income detected, anomaly).
-- Emitted to nexora.insights.alerts for the notification pipeline and stored
-- here so the app can list recent insights even after the SSE is gone.
CREATE TABLE IF NOT EXISTS insights_alerts (
  account_id UUID,
  alert_id UUID,
  alert_type TEXT,
  title TEXT,
  body TEXT,
  payload TEXT,
  created_at TIMESTAMP,
  PRIMARY KEY (account_id, created_at, alert_id)
) WITH CLUSTERING ORDER BY (created_at DESC, alert_id ASC);

-- ── Ledger invariant monitor ───────────────────────────────────────────────
-- Continuous money-correctness guard: a per-account freeze applied when the
-- monitor detects a balance-chain or recompute violation, plus the event
-- trail (VIOLATION / CLEARED) used to reconstruct what happened and when.
-- Bookings against a frozen account are refused until an engineer repairs the
-- ledger and clears the guard.
CREATE TABLE IF NOT EXISTS ledger_integrity_guards (
  account_id UUID PRIMARY KEY,
  frozen BOOLEAN,
  reason TEXT,
  incident_id UUID,
  detected_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ledger_integrity_events (
  account_id UUID,
  event_id UUID,
  event_type TEXT,
  message TEXT,
  detail TEXT,
  detected_at TIMESTAMP,
  cleared_at TIMESTAMP,
  PRIMARY KEY (account_id, detected_at, event_id)
) WITH CLUSTERING ORDER BY (detected_at DESC, event_id ASC);

-- ── Disputes (chargeback orchestration) ────────────────────────────────────
-- dispute_cases: the long-running case aggregate. lifecycle / evidence /
-- events are separate tables so the case list read is a single partition.
CREATE TABLE IF NOT EXISTS dispute_cases (
  case_id UUID PRIMARY KEY,
  user_id UUID,
  account_id UUID,
  entry_id UUID,
  transaction_id UUID,
  amount BIGINT,
  currency TEXT,
  merchant TEXT,
  category TEXT,
  reason TEXT,
  description TEXT,
  status TEXT,
  stage TEXT,
  lifecycle_stage TEXT,
  eligibility TEXT,
  eligible BOOLEAN,
  ineligibility_reason TEXT,
  resolution TEXT,
  provisional_credit BOOLEAN,
  deadline TIMESTAMP,
  resolved_at TIMESTAMP,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

-- Cases by user for the app's dispute list (newest first).
CREATE TABLE IF NOT EXISTS dispute_cases_by_user (
  user_id UUID,
  created_at TIMESTAMP,
  case_id UUID,
  status TEXT,
  lifecycle_stage TEXT,
  amount BIGINT,
  currency TEXT,
  merchant TEXT,
  category TEXT,
  reason TEXT,
  updated_at TIMESTAMP,
  PRIMARY KEY (user_id, created_at, case_id)
) WITH CLUSTERING ORDER BY (created_at DESC, case_id ASC);

-- Evidence documents attached to a case (type + text + base64 payload).
CREATE TABLE IF NOT EXISTS dispute_evidence (
  case_id UUID,
  evidence_id UUID,
  evidence_type TEXT,
  filename TEXT,
  content TEXT,
  uploaded_by UUID,
  created_at TIMESTAMP,
  PRIMARY KEY (case_id, evidence_id)
) WITH CLUSTERING ORDER BY (evidence_id ASC);

-- Append-only audit trail of every case transition/action.
CREATE TABLE IF NOT EXISTS dispute_events (
  case_id UUID,
  event_id UUID,
  event_type TEXT,
  actor TEXT,
  note TEXT,
  created_at TIMESTAMP,
  PRIMARY KEY (case_id, event_id)
) WITH CLUSTERING ORDER BY (event_id ASC);

-- ── Delegated access + consent ─────────────────────────────────────────────
-- Time-bound, capability-scoped grants. Enforced by the identity service on
-- login and by services on every scoped read; every use is audited.
CREATE TABLE IF NOT EXISTS delegation_grants (
  grant_id UUID PRIMARY KEY,
  owner_user_id UUID,
  delegate_email TEXT,
  delegate_user_id UUID,
  label TEXT,
  scopes TEXT,
  status TEXT,
  starts_at TIMESTAMP,
  expires_at TIMESTAMP,
  created_at TIMESTAMP,
  revoked_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS delegation_grants_by_owner (
  owner_user_id UUID,
  created_at TIMESTAMP,
  grant_id UUID,
  delegate_email TEXT,
  label TEXT,
  scopes TEXT,
  status TEXT,
  starts_at TIMESTAMP,
  expires_at TIMESTAMP,
  PRIMARY KEY (owner_user_id, created_at, grant_id)
) WITH CLUSTERING ORDER BY (created_at DESC, grant_id ASC);

-- Every scoped access under a grant (who, what, when, from where).
CREATE TABLE IF NOT EXISTS delegation_audit (
  grant_id UUID,
  used_at TIMESTAMP,
  audit_id UUID,
  delegate_user_id UUID,
  action TEXT,
  resource TEXT,
  allowed BOOLEAN,
  ip_address TEXT,
  PRIMARY KEY (grant_id, used_at, audit_id)
) WITH CLUSTERING ORDER BY (used_at DESC, audit_id ASC);

-- Notifications ─────────────────────────────────────────────────────────────
-- notifications: entity store keyed by id (get + status updates). Metadata is
-- an opaque JSON document (raw event payload) stored as TEXT; it feeds the
-- mobile feed (amount, merchant, balance_after...) without extra joins.
CREATE TABLE IF NOT EXISTS notifications (
  notification_id UUID PRIMARY KEY,
  user_id UUID,
  notification_type TEXT,
  channel TEXT,
  title TEXT,
  body TEXT,
  metadata TEXT,
  status TEXT,
  created_at TIMESTAMP,
  sent_at TIMESTAMP,
  read_at TIMESTAMP
);

-- notifications_by_user: query-first projection for "recent notifications",
-- newest first (denormalised copy maintained by the notification service).
CREATE TABLE IF NOT EXISTS notifications_by_user (
  user_id UUID,
  created_at TIMESTAMP,
  notification_id UUID,
  notification_type TEXT,
  channel TEXT,
  title TEXT,
  body TEXT,
  metadata TEXT,
  status TEXT,
  sent_at TIMESTAMP,
  read_at TIMESTAMP,
  PRIMARY KEY (user_id, created_at, notification_id)
) WITH CLUSTERING ORDER BY (created_at DESC, notification_id ASC);

-- ── Fraud Analyses ───────────────────────────────────────────────────────────
-- One row per evaluated attempt. The auth columns (amount, merchant, city,
-- country, lat/lon) make every decision traceable AND feed the next real-time
-- decision: velocity, amount-out-of-pattern and geo-anomaly checks run over
-- this real history instead of caller-supplied snapshots.
CREATE TABLE IF NOT EXISTS fraud_analyses (
  analysis_id UUID PRIMARY KEY,
  payment_id UUID,
  user_id UUID,
  account_id UUID,
  risk_score DOUBLE,
  risk_action TEXT,
  risk_level TEXT,
  risk_reasons TEXT,
  device_id TEXT,
  ip_address TEXT,
  geo_location TEXT,
  signals TEXT,
  amount BIGINT,
  currency TEXT,
  merchant TEXT,
  merchant_category TEXT,
  merchant_city TEXT,
  merchant_country TEXT,
  latitude DOUBLE,
  longitude DOUBLE,
  created_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_fraud_analyses_payment
  ON fraud_analyses (payment_id);
CREATE INDEX IF NOT EXISTS idx_fraud_analyses_user
  ON fraud_analyses (user_id);

-- ── Audit ────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS audit_events (
  event_id UUID PRIMARY KEY,
  actor TEXT,
  action TEXT,
  resource_type TEXT,
  resource_id TEXT,
  details TEXT,
  request_id TEXT,
  trace_id TEXT,
  ip_address TEXT,
  user_agent TEXT,
  created_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS audit_logs (
  log_id UUID PRIMARY KEY,
  user_id UUID,
  action TEXT,
  resource_type TEXT,
  resource_id TEXT,
  details TEXT,
  ip_address TEXT,
  user_agent TEXT,
  created_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_user
  ON audit_logs (user_id);

-- ── Reconciliation ───────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS reconciliation_cases (
  case_id UUID PRIMARY KEY,
  payment_id UUID,
  internal_state TEXT,
  external_state TEXT,
  internal_amount BIGINT,
  external_amount BIGINT,
  currency TEXT,
  status TEXT,
  resolution TEXT,
  discrepancy_reason TEXT,
  provider_ref TEXT,
  attempt_count INT,
  max_attempts INT,
  created_at TIMESTAMP,
  updated_at TIMESTAMP,
  resolved_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_recon_cases_payment
  ON reconciliation_cases (payment_id);

CREATE TABLE IF NOT EXISTS reconciliation_records (
  record_id UUID PRIMARY KEY,
  transaction_id UUID,
  source TEXT,
  target TEXT,
  amount BIGINT,
  currency TEXT,
  status TEXT,
  discrepancy_reason TEXT,
  created_at TIMESTAMP,
  resolved_at TIMESTAMP
);

-- ── Policies ─────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS policies (
  policy_id UUID PRIMARY KEY,
  name TEXT,
  description TEXT,
  policy_type TEXT,
  scope TEXT,
  status TEXT,
  rules TEXT,
  enabled BOOLEAN,
  version INT,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

-- Feature flags: progressive rollout + auto-rollback (policy-service).
-- Assignment is deterministic: hash(key, user_id) % 100 < rollout_pct.
CREATE TABLE IF NOT EXISTS feature_flags (
  flag_id UUID PRIMARY KEY,
  key TEXT,
  description TEXT,
  enabled BOOLEAN,
  rollout_pct INT,
  cohort_constraint TEXT,
  baseline_error_pct DOUBLE,
  max_error_pct DOUBLE,
  max_latency_ms DOUBLE,
  error_pct DOUBLE,
  latency_ms DOUBLE,
  rolled_back BOOLEAN,
  rollback_reason TEXT,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_feature_flags_key
  ON feature_flags (key);

-- ── Time-Travel Compliance Engine (policy-service) ─────────────────────────
-- Append-only policy versions with [valid_from, valid_to) windows. Old
-- versions are NEVER mutated; a semantic change ships as a new version.
CREATE TABLE IF NOT EXISTS policy_versions (
  policy_id UUID,
  version INT,
  status TEXT,
  rules TEXT,
  name TEXT,
  enabled BOOLEAN,
  valid_from TIMESTAMP,
  valid_to TIMESTAMP,
  PRIMARY KEY (policy_id, version)
);

-- Customer state snapshots, newest-first clustering so "LIMIT 1" with
-- captured_at <= ? returns the snapshot in force at that instant.
CREATE TABLE IF NOT EXISTS customer_state_snapshots (
  user_id UUID,
  captured_at TIMESTAMP,
  snapshot_id UUID,
  kyc_tier INT,
  device_known BOOLEAN,
  account_age_days INT,
  flagged_risk BOOLEAN,
  PRIMARY KEY (user_id, captured_at)
) WITH CLUSTERING ORDER BY (captured_at DESC);

-- Historical decision records keyed for lookup by payment.
CREATE TABLE IF NOT EXISTS historical_decisions (
  payment_id TEXT PRIMARY KEY,
  record_id UUID,
  decided_at TIMESTAMP,
  final_decision TEXT,
  policy_versions LIST<TEXT>,
  snapshot_id UUID,
  replay_hash TEXT,
  created_at TIMESTAMP
);

-- ── Incidents ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS incidents (
  incident_id UUID PRIMARY KEY,
  title TEXT,
  description TEXT,
  severity TEXT,
  status TEXT,
  affected_services LIST<TEXT>,
  created_by UUID,
  assigned_to UUID,
  resolution TEXT,
  created_at TIMESTAMP,
  updated_at TIMESTAMP,
  resolved_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS incident_timeline (
  incident_id UUID,
  event_id UUID,
  event_type TEXT,
  description TEXT,
  actor TEXT,
  timestamp TIMESTAMP,
  PRIMARY KEY (incident_id, event_id)
);

-- ── Replay & Simulation Results ──────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS replay_results (
  replay_id UUID PRIMARY KEY,
  original_result TEXT,
  replayed_result TEXT,
  differences TEXT,
  total_events BIGINT,
  deterministic BOOLEAN,
  replayed_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS simulation_results (
  result_id UUID PRIMARY KEY,
  would_succeed BOOLEAN,
  predicted_balance BIGINT,
  predicted_available BIGINT,
  risk_score DOUBLE,
  reasons TEXT,
  simulated_at TIMESTAMP
);

-- ── Payment booking marks ────────────────────────────────────────────────────
-- LWT gate used by the ledger payment consumer: the first lifecycle event for
-- a payment claims the row (INSERT IF NOT EXISTS) and books the double entry;
-- later events (settled after confirmed) see applied=false and skip. This is
-- deterministic idempotency where a secondary-index read can race.
CREATE TABLE IF NOT EXISTS ledger_payment_marks (
  payment_id UUID PRIMARY KEY,
  idempotency_key TEXT,
  event_type TEXT,
  booked_at TIMESTAMP
);

-- ── Outbox (transactional outbox for reliable event publishing) ───────────────
-- Domain events are written atomically with their aggregate state change (a
-- logged Cassandra batch, see ADR-005) and drained to Kafka by the outbox
-- relay (shared/outbox). PK on event_id makes claim/mark point updates; the
-- status index backs the PENDING claim query (ALLOW FILTERING at dev scale).
CREATE TABLE IF NOT EXISTS outbox_events (
  event_id UUID PRIMARY KEY,
  aggregate_id TEXT,
  event_type TEXT,
  topic TEXT,
  correlation_id TEXT,
  causation_id TEXT,
  producer TEXT,
  payload BLOB,
  status TEXT,
  attempts INT,
  last_error TEXT,
  created_at TIMESTAMP,
  published_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_outbox_status
  ON outbox_events (status);

-- ── Schema Version Tracking ──────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS schema_versions (
  version INT PRIMARY KEY,
  description TEXT,
  applied_at TIMESTAMP,
  applied_by TEXT
);
