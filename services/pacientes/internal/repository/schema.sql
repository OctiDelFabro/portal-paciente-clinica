CREATE TABLE IF NOT EXISTS patients (
  id VARCHAR(64) PRIMARY KEY,
  email VARCHAR(254) NULL,
  password_hash VARCHAR(100) NULL,
  full_name VARCHAR(120) NOT NULL,
  document_number VARCHAR(20) NULL,
  document_key VARCHAR(20) GENERATED ALWAYS AS (UPPER(TRIM(document_number))) STORED,
  birth_date DATE NULL,
  enabled BOOLEAN NOT NULL DEFAULT FALSE,
  responsible_id VARCHAR(64) NULL,
  created_at DATETIME(3) NOT NULL,
  role VARCHAR(20) NOT NULL DEFAULT 'patient',
  UNIQUE KEY uq_email (email),
  INDEX idx_document_key (role,document_key)
);

-- Serializes identity checks without deleting or choosing a survivor among
-- legacy duplicate fichas. New creates/updates cannot introduce duplicates.
CREATE TABLE IF NOT EXISTS patient_document_locks(document_key VARCHAR(20) PRIMARY KEY);
