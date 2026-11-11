CREATE TABLE IF NOT EXISTS appointments (
  id CHAR(36) PRIMARY KEY,
  patient_id VARCHAR(64) NOT NULL,
  doctor_id VARCHAR(64) NOT NULL,
  start_at DATETIME(3) NOT NULL,
  end_at DATETIME(3) NOT NULL,
  status VARCHAR(16) NOT NULL,
  version INT NOT NULL,
  cancel_reason VARCHAR(200) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  INDEX idx_doctor_time (doctor_id, status, start_at),
  INDEX idx_patient_time (patient_id, status, start_at)
);

CREATE TABLE IF NOT EXISTS booking_locks (
  resource_key VARCHAR(80) PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS appointment_reminders(appointment_id CHAR(36) PRIMARY KEY);

CREATE TABLE IF NOT EXISTS doctor_agendas (
 doctor_id VARCHAR(64) PRIMARY KEY,
 duration_minutes INT NOT NULL,
 weekdays JSON NOT NULL,
 open_minutes INT NOT NULL,
 close_minutes INT NOT NULL
);
CREATE TABLE IF NOT EXISTS agenda_blocks (
 id CHAR(36) PRIMARY KEY,
 doctor_id VARCHAR(64) NOT NULL,
 start_at DATETIME(3) NOT NULL,
 end_at DATETIME(3) NOT NULL,
 reason VARCHAR(200) NOT NULL,
 active BOOLEAN NOT NULL DEFAULT TRUE,
 INDEX idx_blocks(doctor_id,active,start_at)
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

-- Reservas creadas por consumidores externos (ADR-008): duenio, clave de
-- idempotencia permanente y precio cobrado al momento de reservar.
CREATE TABLE IF NOT EXISTS partner_bookings (
  appointment_id CHAR(36) PRIMARY KEY,
  consumer VARCHAR(64) NOT NULL,
  idem_key VARCHAR(128) NOT NULL,
  fingerprint CHAR(64) NOT NULL,
  price_cents BIGINT NOT NULL,
  currency CHAR(3) NOT NULL,
  booked_at DATETIME(3) NOT NULL,
  UNIQUE KEY uq_consumer_key (consumer, idem_key),
  INDEX idx_consumer_period (consumer, booked_at)
);
