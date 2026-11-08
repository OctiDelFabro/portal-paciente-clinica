-- Medicos is the source of truth for doctor profiles (ADR-003, ADR-006).
CREATE TABLE IF NOT EXISTS specialties (
  code VARCHAR(64) PRIMARY KEY,
  name VARCHAR(120) NOT NULL
);

CREATE TABLE IF NOT EXISTS doctors (
  id VARCHAR(64) PRIMARY KEY,
  full_name VARCHAR(160) NOT NULL,
  accepts_minors BOOLEAN NOT NULL,
  active BOOLEAN NOT NULL,
  profile_version BIGINT NOT NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
);

-- Many-to-many between doctors and specialties; position keeps the order chosen by the admin.
CREATE TABLE IF NOT EXISTS doctor_specialties (
  doctor_id VARCHAR(64) NOT NULL,
  specialty_code VARCHAR(64) NOT NULL,
  position INT NOT NULL,
  PRIMARY KEY (doctor_id, specialty_code),
  CONSTRAINT fk_ds_doctor FOREIGN KEY (doctor_id) REFERENCES doctors (id),
  CONSTRAINT fk_ds_specialty FOREIGN KEY (specialty_code) REFERENCES specialties (code)
);

-- Derived projection of Turnos events, used only to compute nextAvailableAt.
-- Turnos stays the authority for bookings; this table is rebuildable from its events.
CREATE TABLE IF NOT EXISTS doctor_bookings (
  turn_id VARCHAR(64) PRIMARY KEY,
  doctor_id VARCHAR(64) NOT NULL,
  start_at DATETIME(3) NOT NULL,
  end_at DATETIME(3) NOT NULL,
  active BOOLEAN NOT NULL,
  appointment_version INT NOT NULL,
  INDEX idx_doctor_active (doctor_id, active, end_at)
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
