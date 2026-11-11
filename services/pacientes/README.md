# Pacientes

Patient identity for the portal: self registration, login and the patient profile. Layered architecture (ADR-002): `internal/api` (HTTP) → `internal/service` (use cases, JWT, login limiter) → `internal/repository` (MySQL/InnoDB, ADR-003). Pacientes issues the access token; the gateway validates it (assumption A1, ADR-014).

## Endpoints

Behind the gateway these live under `/api/pacientes/*` (prefix stripped). `/auth/register` and `/auth/login` need no token; every other route receives `X-Patient-Id` and `X-User-Role` from the gateway, which strips any client copy.

| Method and path | Result |
|---|---|
| `POST /auth/register` | `{email, password, fullName, documentNumber, birthDate}` → `201 {patientId}`. The new patient is pending verification by reception (`enabled: false`, RF-13). |
| `POST /auth/login` | `{email, password}` → `200 {accessToken, tokenType: "Bearer", expiresIn, patient: {patientId, fullName, email}}` |
| `GET /me` | Profile of `X-Patient-Id`: `patientId, fullName, email, documentNumber, birthDate, enabled, responsiblePatientId`. `401` without the header. |
| `GET /patients/{id}/contact` | `{patientId, fullName, email, responsiblePatientId?}` for internal services. `email` is `null` for a minor without email; notify `responsiblePatientId` instead. A request carrying `X-Patient-Id` (that is, from the gateway) may only read its own contact (`403` otherwise). |
| `GET /dependents` | Role `patient`: the minors linked to the caller as responsible adult. |
| `GET /patients` | Reception or admin: patient directory for verification. |
| `POST /patients` | Reception or admin: create a patient (for example a minor without email). |
| `PUT /patients/{id}` | Reception or admin: edit profile data. |
| `PATCH /patients/{id}/verification` | Reception or admin: `{enabled}` verifies or revokes an account (RF-13); `422` without `enabled`. |
| `PATCH /patients/{id}/responsible` | Reception or admin: `{responsiblePatientId}` links or unlinks the responsible adult of a minor. |
| `GET /internal/patients/{id}` | Internal services only, with `X-Service-Key`; not exposed by the gateway. |
| `GET /healthz` | `200` when MySQL answers, `503` otherwise. |
| `GET /metrics` | Prometheus metrics. |

Errors use the shared shape `{"error": {"code", "message"}}`:

| Status | Code | When |
|---|---|---|
| 400 | `INVALID_JSON` | Malformed body, unknown fields or more than one object. |
| 401 | `INVALID_CREDENTIALS` | Unknown email or wrong password (same body for both). |
| 401 | `UNAUTHORIZED` | `GET /me` without `X-Patient-Id`. |
| 403 | `FORBIDDEN` | Contact of another patient requested through the gateway. |
| 404 | `NOT_FOUND` | Unknown patient. |
| 409 | `EMAIL_TAKEN` | Email already registered (case-insensitive). |
| 413 | `PAYLOAD_TOO_LARGE` | Body over 16 KiB. |
| 415 | `UNSUPPORTED_MEDIA_TYPE` | Content-Type is not `application/json`. |
| 422 | `VALIDATION_ERROR` | Password under 8 characters or over 72 bytes, invalid email, empty name, document not 6–20 letters or digits, birth date not a past `YYYY-MM-DD`. |
| 429 | `RATE_LIMITED` | 5 failed logins for the same email within 15 minutes; `Retry-After` gives the seconds left. |
| 503 | `SERVICE_UNAVAILABLE` | MySQL unavailable. |

## Access token (JWT)

HS256, signed with `JWT_SECRET` (shared with the gateway). Claims:

| Claim | Value |
|---|---|
| `sub` | patient ID (`pat-…`) |
| `role` | `patient` |
| `iss` | `pacientes` |
| `iat` | issue time (Unix seconds) |
| `exp` | `iat` + `JWT_TTL` |

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `PACIENTES_DB_DSN` | required | e.g. `pacientes:<password>@tcp(mysql:3306)/pacientes` |
| `JWT_SECRET` | required | At least 32 bytes; the service refuses to start otherwise. |
| `JWT_TTL` | `1h` | Go duration (`30m`, `2h`). |
| `DEMO_PATIENT_PASSWORD` | empty | Password for the demo accounts. Empty: demo profiles are created without login (logged at INFO). At least 8 characters when set. |
| `HTTP_ADDR` | `127.0.0.1:8082` | |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | | Tracing, as in the other services (`internal/platform/obs`). |

The schema (`internal/repository/schema.sql`) is applied on startup with `CREATE TABLE IF NOT EXISTS`.

## Demo data

Created on startup if missing, with the IDs used by Turnos and the emails used by Notificaciones. All data is fictitious.

| ID | Name | Email | Enabled | Notes |
|---|---|---|---|---|
| `pat-001` | Lucia Fernandez | `paciente1@example.test` | yes | Responsible adult of `pat-003`. |
| `pat-002` | Martin Perez | `paciente2@example.test` | no | Registered, not enabled by reception (RF-13). |
| `pat-003` | Tomas Fernandez | — | yes | Minor without email or login. |

## Security notes

- Passwords are stored only as bcrypt hashes and never returned or logged; emails are not logged either (logs carry `patient_id` only).
- An unknown email still runs a bcrypt comparison, so response time does not reveal which emails are registered.
- The login limiter is in memory and **per instance**: with N replicas an attacker gets up to N×5 attempts per email and window, and a restart clears it. It tracks at most 10,000 emails; beyond that the gateway rate limit is the protection. A shared store (Redis) would remove both limits.

## Observability

Logs, traces and RED metrics come from `internal/platform/obs`, like the other services. Service-specific metric: `pacientes_auth_total{operation, outcome}` with `operation` in `register|login` and `outcome` in `created|email_taken|invalid|bad_request|success|locked|unavailable`. Fixed label values keep cardinality bounded; a rise of `login/invalid` or `login/locked` points to brute force, while `unavailable` points to the database.

## Tests

```sh
go test ./services/pacientes/...
go test -tags integration ./services/pacientes/...   # needs Docker (testcontainers MySQL)
```
