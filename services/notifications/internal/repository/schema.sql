CREATE TABLE IF NOT EXISTS notifications (
  id CHAR(36) PRIMARY KEY,
  consumer VARCHAR(64) NOT NULL,
  source VARCHAR(16) NOT NULL,
  template_id VARCHAR(64) NOT NULL,
  recipient VARCHAR(254) NOT NULL,
  params JSON NOT NULL,
  status VARCHAR(16) NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  next_attempt_at DATETIME(3) NOT NULL,
  trace_headers JSON NOT NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  INDEX idx_due (status, next_attempt_at)
);

CREATE TABLE IF NOT EXISTS idempotency_keys (
  consumer VARCHAR(64) NOT NULL,
  idem_key VARCHAR(128) NOT NULL,
  fingerprint CHAR(64) NOT NULL,
  notification_id CHAR(36) NOT NULL,
  accepted_at DATETIME(3) NOT NULL,
  expires_at DATETIME(3) NOT NULL,
  PRIMARY KEY (consumer, idem_key)
);

CREATE TABLE IF NOT EXISTS inbox (
  event_id CHAR(36) PRIMARY KEY,
  event_type VARCHAR(64) NOT NULL,
  received_at DATETIME(3) NOT NULL
);

CREATE TABLE IF NOT EXISTS portal_notices (
  id CHAR(36) PRIMARY KEY,
  patient_id VARCHAR(64) NOT NULL,
  turn_id VARCHAR(64) NOT NULL,
  kind VARCHAR(32) NOT NULL,
  created_at DATETIME(3) NOT NULL,
  read_at DATETIME(3) NULL,
  INDEX idx_patient (patient_id, created_at)
);
