CREATE TABLE IF NOT EXISTS patients (
  id VARCHAR(64) PRIMARY KEY,
  email VARCHAR(254) NULL,
  password_hash VARCHAR(100) NULL,
  full_name VARCHAR(120) NOT NULL,
  document_number VARCHAR(20) NULL,
  birth_date DATE NULL,
  enabled BOOLEAN NOT NULL DEFAULT FALSE,
  responsible_id VARCHAR(64) NULL,
  created_at DATETIME(3) NOT NULL,
  role VARCHAR(20) NOT NULL DEFAULT 'patient',
  UNIQUE KEY uq_email (email)
);
