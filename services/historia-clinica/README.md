# Historia clínica

Per-patient clinical timeline on MongoDB (ADR-003), fed by Turnos domain events over RabbitMQ (ADR-005). Layered architecture (ADR-002): `internal/api` (HTTP + AMQP consumer) → `internal/service` (rules) → `internal/repository` (MongoDB).

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `HTTP_ADDR` | `127.0.0.1:8084` | Listen address |
| `MONGO_URI` | `mongodb://127.0.0.1:27017` | MongoDB connection string |
| `MONGO_DB` | `historia_clinica` | Database name |
| `PACIENTES_URL` | `http://127.0.0.1:8082` | Current verification and responsible adult lookup |
| `TURNOS_URLS` | `http://127.0.0.1:8081` | Uncancelled doctor/patient relationships; comma-separated replicas |
| `INTERNAL_API_KEY` | required, at least 32 bytes | Backend service authentication, not forwarded by the browser |
| `RABBITMQ_URL` | `amqp://guest:guest@127.0.0.1:5672/` | Broker URL |

On startup the service retries the MongoDB connection (30 attempts, 2 s apart), creates the `encounters` validator and indexes, then starts the consumer and the HTTP server.

## HTTP API

Reached through the gateway as `/api/historia/*` (prefix stripped, JWT required). The service trusts `X-Patient-Id` and `X-User-Role` set by the gateway. Errors use `{"error":{"code":"...","message":"..."}}`.

| Method and path | Who | Result |
|---|---|---|
| `GET /healthz` | anyone | `200 {"status":"ok","mongo":"up"}` or `503 {"status":"degraded","mongo":"down"}` |
| `GET /records/me` | verified patient | `200 {"patientId","records":[...]}`: the caller's encounters, newest `startAt` first, with notes; `401` without `X-Patient-Id` |
| `GET /records/patients/{patientId}/timeline` | verified guardian of a minor or doctor with an uncancelled appointment | `200` timeline, `403` without relationship, `503` if authorization dependencies fail |
| `POST /records/{appointmentId}/notes` | `X-User-Role: doctor` | body `{"text":"..."}` (1–4000 characters); `201` note; `401` no identity; `403` other roles; `400` invalid JSON; `422` invalid text; `404` unknown appointment; `409 ENCOUNTER_CANCELLED` |
| `GET /metrics` | Prometheus | `events_consumed_total{result}`, `historia_dlq_depth`, HTTP metrics |

## Data model

- `encounters`: one document per appointment, `_id` = appointmentId; `patientId`, `doctorId`, `startAt`, `status` (`scheduled` or `cancelled`), `cancelReason`, `notes[]`, `schemaVersion`, `createdAt`, `updatedAt`. A `$jsonSchema` validator enforces the mandatory fields; notes only require `id`, `text` and `createdAt`, so they can grow new fields without a migration. Index `patient_timeline` on `(patientId, startAt desc)` serves `GET /records/me`.
- `processed_events`: inbox of consumed events, `_id` = eventId (the primary key is the uniqueness constraint).

## Events

Queue `historia-clinica.turnos.v1`, bound to exchange `portal.events.v1` with `turnos.reservado.v1` and `turnos.cancelado.v1`.

- Booking creates the encounter as `scheduled` (insert only). Cancellation sets `cancelled`, creating the encounter if it does not exist yet. Cancellation is terminal: a booking that arrives after it (out of order) does not resurrect the encounter.
- Idempotency: an event id already in `processed_events` is acknowledged without changes. MongoDB runs standalone (no transactions), so the encounter write happens first and the inbox marker after it; both writes are idempotent, so a crash in between only replays an operation that converges to the same state.
- Unprocessable messages (invalid JSON, unknown type or version, missing ids, invalid `startAt`, cancellation without `reasonCode`) go straight to `historia-clinica.turnos.v1.dlq` through `portal.events.dlx`, without retry.
- Transient MongoDB errors are retried after 5 s, 30 s and 120 s (`.retry.5s`, `.retry.30s`, `.retry.120s` TTL queues), then dead-lettered. The original is acknowledged only after the broker confirms the retry copy.
- Trace context and correlation id are extracted from AMQP headers (`internal/platform/messaging`).

## Tests

```sh
go test ./services/historia-clinica/...                      # unit tests with fakes
go test -tags integration ./services/historia-clinica/...    # real MongoDB and RabbitMQ (Docker)
```

## Known limitations

- Notes require the assigned doctor identity, current verification and an uncancelled clinical relationship. Reception and administration cannot read clinical content.
- `GET /records/me` is the caller’s timeline. The patient-specific endpoint also allows a verified adult with a currently verified minor relationship; the general minor selector in the frontend remains pending.
- Notes cannot be corrected or deleted; there is no retention or backup policy yet (ADR-003).
- Rescheduling events are not consumed because Turnos does not publish them yet.
