CREATE KEYSPACE IF NOT EXISTS nexora
  WITH replication = {
    'class': 'SimpleStrategy',
    'replication_factor': 1
  }
  AND durable_writes = true;

USE nexora;

-- ── Users ────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS users (
  user_id UUID PRIMARY KEY,
  email TEXT,
  phone TEXT,
  password_hash TEXT,
  first_name TEXT,
  last_name TEXT,
  date_of_birth DATE,
  status TEXT,
  kyc_status TEXT,
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

-- ── Identity / Auth ──────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS auth_credentials (
  user_id UUID PRIMARY KEY,
  password_hash TEXT,
  mfa_enabled BOOLEAN,
  mfa_secret TEXT,
  failed_login_attempts INT,
  locked_until TIMESTAMP,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS refresh_tokens (
  token_id UUID PRIMARY KEY,
  user_id UUID,
  token_hash TEXT,
  device_id TEXT,
  expires_at TIMESTAMP,
  created_at TIMESTAMP,
  revoked BOOLEAN
);

CREATE TABLE IF NOT EXISTS otp_codes (
  request_id UUID PRIMARY KEY,
  user_id UUID,
  code TEXT,
  purpose TEXT,
  expires_at TIMESTAMP,
  used BOOLEAN,
  created_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
  session_id UUID PRIMARY KEY,
  user_id UUID,
  access_token_hash TEXT,
  device_info TEXT,
  ip_address TEXT,
  expires_at TIMESTAMP,
  created_at TIMESTAMP,
  revoked BOOLEAN
);

-- ── Accounts ─────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS accounts (
  account_id UUID PRIMARY KEY,
  user_id UUID,
  account_type TEXT,
  currency TEXT,
  balance BIGINT,
  status TEXT,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS accounts_by_user (
  user_id UUID,
  account_id UUID,
  account_type TEXT,
  currency TEXT,
  status TEXT,
  created_at TIMESTAMP,
  PRIMARY KEY (user_id, account_id)
);

-- ── Ledger ───────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS ledger_entries (
  entry_id UUID PRIMARY KEY,
  account_id UUID,
  entry_type TEXT,
  amount BIGINT,
  balance_after BIGINT,
  currency TEXT,
  reference TEXT,
  description TEXT,
  metadata MAP<TEXT, TEXT>,
  created_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ledger_entries_by_account (
  account_id UUID,
  created_at TIMESTAMP,
  entry_id UUID,
  entry_type TEXT,
  amount BIGINT,
  balance_after BIGINT,
  PRIMARY KEY (account_id, created_at, entry_id)
);

-- ── Transactions ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS transactions (
  transaction_id UUID PRIMARY KEY,
  account_id UUID,
  type TEXT,
  amount BIGINT,
  currency TEXT,
  status TEXT,
  reference TEXT,
  counterparty TEXT,
  metadata MAP<TEXT, TEXT>,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS transactions_by_account (
  account_id UUID,
  created_at TIMESTAMP,
  transaction_id UUID,
  type TEXT,
  amount BIGINT,
  status TEXT,
  PRIMARY KEY (account_id, created_at, transaction_id)
);

-- ── Payments ─────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS payments (
  payment_id UUID PRIMARY KEY,
  account_id UUID,
  payee_name TEXT,
  payee_account TEXT,
  payee_sort_code TEXT,
  amount BIGINT,
  currency TEXT,
  status TEXT,
  payment_type TEXT,
  reference TEXT,
  scheduled_date TIMESTAMP,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS payments_by_account (
  account_id UUID,
  created_at TIMESTAMP,
  payment_id UUID,
  status TEXT,
  PRIMARY KEY (account_id, created_at, payment_id)
);

-- ── Transfers ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS transfers (
  transfer_id UUID PRIMARY KEY,
  from_account_id UUID,
  to_account_id UUID,
  amount BIGINT,
  currency TEXT,
  status TEXT,
  reference TEXT,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

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
  expires_at DATE,
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

-- ── Pots (Savings Pots) ─────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS pots (
  pot_id UUID PRIMARY KEY,
  user_id UUID,
  account_id UUID,
  name TEXT,
  target_amount BIGINT,
  current_amount BIGINT,
  currency TEXT,
  status TEXT,
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

-- ── Notifications ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS notifications (
  notification_id UUID PRIMARY KEY,
  user_id UUID,
  type TEXT,
  title TEXT,
  body TEXT,
  channel TEXT,
  status TEXT,
  read_at TIMESTAMP,
  created_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS notifications_by_user (
  user_id UUID,
  created_at TIMESTAMP,
  notification_id UUID,
  type TEXT,
  status TEXT,
  PRIMARY KEY (user_id, created_at, notification_id)
);

-- ── Fraud Alerts ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS fraud_alerts (
  alert_id UUID PRIMARY KEY,
  user_id UUID,
  account_id UUID,
  transaction_id UUID,
  risk_score BIGINT,
  risk_factors LIST<TEXT>,
  status TEXT,
  action_taken TEXT,
  created_at TIMESTAMP,
  resolved_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS fraud_alerts_by_user (
  user_id UUID,
  created_at TIMESTAMP,
  alert_id UUID,
  risk_score BIGINT,
  status TEXT,
  PRIMARY KEY (user_id, created_at, alert_id)
);

-- ── Audit Log ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS audit_log (
  log_id UUID PRIMARY KEY,
  user_id UUID,
  service TEXT,
  action TEXT,
  resource_type TEXT,
  resource_id TEXT,
  ip_address TEXT,
  user_agent TEXT,
  metadata MAP<TEXT, TEXT>,
  created_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS audit_log_by_user (
  user_id UUID,
  created_at TIMESTAMP,
  log_id UUID,
  service TEXT,
  action TEXT,
  PRIMARY KEY (user_id, created_at, log_id)
);

-- ── Policies ─────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS policies (
  policy_id UUID PRIMARY KEY,
  name TEXT,
  description TEXT,
  rules TEXT,
  status TEXT,
  version INT,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS policies_by_user (
  user_id UUID,
  policy_id UUID,
  assigned_at TIMESTAMP,
  status TEXT,
  PRIMARY KEY (user_id, policy_id)
);

-- ── Incidents ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS incidents (
  incident_id UUID PRIMARY KEY,
  title TEXT,
  description TEXT,
  severity TEXT,
  status TEXT,
  assigned_to UUID,
  affected_services LIST<TEXT>,
  created_at TIMESTAMP,
  updated_at TIMESTAMP,
  resolved_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS incidents_by_status (
  status TEXT,
  created_at TIMESTAMP,
  incident_id UUID,
  severity TEXT,
  title TEXT,
  PRIMARY KEY (status, created_at, incident_id)
);

-- ── Reconciliation ───────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS reconciliation_records (
  record_id UUID PRIMARY KEY,
  source TEXT,
  target TEXT,
  amount BIGINT,
  currency TEXT,
  status TEXT,
  reference TEXT,
  created_at TIMESTAMP,
  reconciled_at TIMESTAMP
);

-- ── Simulations ──────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS simulations (
  simulation_id UUID PRIMARY KEY,
  name TEXT,
  description TEXT,
  config TEXT,
  status TEXT,
  result TEXT,
  created_by UUID,
  created_at TIMESTAMP,
  finished_at TIMESTAMP
);

-- ── Replay Events ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS replay_events (
  event_id UUID PRIMARY KEY,
  aggregate_id UUID,
  event_type TEXT,
  payload TEXT,
  sequence_number BIGINT,
  created_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS replay_events_by_aggregate (
  aggregate_id UUID,
  sequence_number BIGINT,
  event_id UUID,
  event_type TEXT,
  payload TEXT,
  created_at TIMESTAMP,
  PRIMARY KEY (aggregate_id, sequence_number, event_id)
);

-- ── Control Plane ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS service_registry (
  service_name TEXT PRIMARY KEY,
  service_version TEXT,
  endpoint TEXT,
  status TEXT,
  health_status TEXT,
  last_health_check TIMESTAMP,
  metadata MAP<TEXT, TEXT>,
  registered_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS feature_flags (
  flag_name TEXT PRIMARY KEY,
  enabled BOOLEAN,
  description TEXT,
  rollout_percentage INT,
  allowed_users LIST<UUID>,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);

-- ── Schema Version Tracking ──────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS schema_versions (
  version INT PRIMARY KEY,
  description TEXT,
  applied_at TIMESTAMP,
  applied_by TEXT
);
