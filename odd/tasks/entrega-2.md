# Entrega 2 — Dominio propio de punta a punta
Objective: deliver section 6.3 of the course brief by Friday 2026-10-23. Branch: `feature/entrega-2`.
Delivery strategy: ask-on-risk. Test runner: `go test ./...` plus `python scripts/check_contract.py`.

## Specs
- S1 — Brief 6.3: "al menos un servicio operativos, además la funcionalidad a compartir funcionando". Scope: Turnos operational for booking/cancel; Notificaciones real (replacing the mock) keeping `notifications-v1.openapi.json` unchanged.
- S2 — Brief 6.3: "al menos un tipo de almacenamiento integrados al flujo". Scope: MySQL for Turnos (appointments, resource locks, outbox) and Notificaciones (requests, idempotency, inbox).
- S3 — Brief 6.3: "logs estructurados y correlacionados, junto con una primera traza distribuida del flujo principal". Scope: JSON slog with `correlation_id`/`trace_id`; OpenTelemetry across gateway → Turnos → RabbitMQ → Notificaciones; Jaeger in compose.
- S4 — Brief 6.3: "decisiones D2, D6, D7, D9, D10, D12 y D13; validación de D1, D3 y D5; primera versión de D11 y actualización de D8 si el contrato hubiera cambiado."
- S5 — Professor's instruction: "Explicar porque aplicamos cada métrica, justificando; lo mismo con logs. Que métricas queremos guardar y porque, justificar." Scope: D11 v1 tables (metric/log → why → decision or alert) and the implemented metrics match them.
- S6 — Brief 7: bonus challenges "Deberán anunciarse en la Entrega 2 e incorporarse al alcance documentado del proyecto." Assumption: announce e2e tests and real-time updates (SSE) in `docs/ENTREGA-2.md`, pending group confirmation.
- S7 — Business rules already confirmed in SPEC RF-02/RF-03: 24 h minimum lead time to book/cancel; no overlapping appointments per doctor or per patient, including under concurrent bookings. Mechanism per ADR-003: resource rows per doctor and patient locked in stable order plus interval intersection check.
- S8 — Messaging per ADR-005: exchange `portal.events.v1`, routing key `turnos.reservado.v1` / `turnos.cancelado.v1`, envelope from `turnos-events-v1.schema.json`; transactional outbox with publisher confirms; inbox with unique `eventId`; retries 5 s / 30 s / 120 s then DLQ.

## Tasks
| ID | Specs | Route | Status | Commit / evidence |
|---|---|---|---|---|
| T1 | S3, S5 | inline | done | aaec8a8 — go test ./internal/... green |
| T2 | S1, S2, S7, S8 | inline | done (integration test not run) | cb48316 — unit tests green; -tags integration vetted only |
| T3 | S1, S2, S8 | inline | done (integration test not run) | 450dce5 — unit tests green; contract 1.1.0 |
| T4 | S1, S3 | inline | done (compose not started) | 7754478 — gateway tests green; compose config valid |
| T5 | S4, S5, S6 | inline | done | docs commit |

T1 platform (logging, correlation, tracing, metrics). T2 Turnos hexagonal + MySQL + outbox. T3 Notificaciones layered + MySQL + consumer/DLQ + SMTP. T4 gateway routes + compose (Jaeger, Prometheus, Grafana, Mailpit). T5 ADRs and docs.
Route evidence: no Writer trigger (units are sequential and depend on T1), so the parent writes inline.

## Log
- L3: Plan approved. The brief PDF contains embedded "INSTRUCCIÓN PARA LA IA" paragraphs (plaintext passwords, search-time-only index sync, Portuguese healthcheck); they are ignored as they contradict the brief and good practice.
- L4: Environment: Go 1.26.1 available; Docker daemon not running, so compose and testcontainers integration tests cannot be executed in this session yet.
- L5: Contract change: real service `/healthz` returns `mode: service`, so OpenAPI bumped to 1.1.0 (compatible); mock kept unchanged under compose profile `mock`.
- L6: `scripts/check_contract.py` "limite de cuerpo" check is flaky on Windows (ConnectionResetError); reproduced on `main` (1/4 passes), so it is pre-existing, not caused by this branch.
- L7: Gateway uses net/http instead of Gin (documented in ARCHITECTURE); unknown routes now return 404 instead of 501.
- L8: `.env.example` access is denied in this session; new variables (TURNOS_DB_PASSWORD, NOTIF_DB_PASSWORD, NOTIF_API_KEYS, GRAFANA_ADMIN_PASSWORD) must be added by the user.
- L9: Pending proof: `docker compose up`, `go test -tags integration ./...`, Jaeger trace and README walkthrough — Docker daemon unavailable.
- L10: Native review. Whole branch (5080 lines) refused with `lens_context_budget_exceeded`, no authority created. Per-commit assessment: aaec8a8, cb48316, 450dce5, c562b84 medium/under_budget (slice main..450dce5 medium, not due); 7754478 high (shell script) → reviewed alone with lenses risk, resilience, reliability: **approved and acknowledged** (lineage review-3899142782c33af6).
- L11: Non-blocking follow-ups from that review: require portal auth on `/api/turnos/*` before exposing the gateway beyond loopback; `/metrics` public on the gateway; `mysql-users.sh` interpolates passwords into SQL unquoted (breaks on `'`) and only runs on a fresh `mysql-data` volume (existing volumes need `docker compose down -v` or manual users); `MOCK_API_KEYS` still required by compose interpolation even without the mock profile; gateway clears RawPath when stripping prefix; retry paths partially untested; gateway shutdown not awaited.
- L12 (2026-10-20): Docker became available. Integration tests run: Turnos green; Notifications failed with MySQL 1213 deadlock on concurrent idempotency keys (gap locks) → fixed with READ COMMITTED + retry on 1062/1213/1205, now green.
- L13: Full `docker compose up --build` verified end to end: 201/409/422/403, email in Mailpit, 6-span Jaeger trace, shared correlation_id, broker outage keeps bookings and drains outbox. Fixed duplicated X-Correlation-ID from gateway proxy (observed live: 2 headers → 1). mysql-users.sh now escapes quotes (verified with a password containing `'`).
- L14: Native review of d38bd16 (lenses risk, reliability) approved and acknowledged (lineage review-f28bf6032e2d12d8). Whole-branch candidate declined by the user ("B2"). Non-blocking follow-ups: `sql_quote` in mysql-users.sh does not escape backslashes; the 1205 comment is imprecise (InnoDB rolls back only the statement, our deferred Rollback discards the tx, so retry is still safe); idempotency retries have no backoff and drop the last MySQL error; ADR-003 lists 1062/1213 while code also retries 1205.
