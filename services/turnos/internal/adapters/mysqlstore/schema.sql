CREATE TABLE IF NOT EXISTS appointments (
  id CHAR(36) PRIMARY KEY,
  patient_id VARCHAR(64) NOT NULL,
  doctor_id VARCHAR(64) NOT NULL,
  start_at DATETIME(3) NOT NULL,
  end_at DATETIME(3) NOT NULL,
  status VARCHAR(16) NOT NULL,
  version INT NOT NULL,
  cancel_reason VARCHAR(32) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  INDEX idx_doctor_time (doctor_id, status, start_at),
  INDEX idx_patient_time (patient_id, status, start_at)
);

CREATE TABLE IF NOT EXISTS booking_locks (
  resource_key VARCHAR(80) PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS outbox (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  event_id CHAR(36) NOT NULL UNIQUE,
  routing_key VARCHAR(64) NOT NULL,
  payload JSON NOT NULL,
  headers JSON NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  published_at DATETIME(3) NULL,
  attempts INT NOT NULL DEFAULT 0,
  INDEX idx_pending (published_at, id)
);
