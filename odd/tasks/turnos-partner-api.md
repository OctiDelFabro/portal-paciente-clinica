# Turnos partner API — published capability for Car Cruds
Objective: publish Turnos (not Notificaciones) as the group's shared capability, with auth, idempotency, billing and consumer docs. Branch: `feature/entrega-2-06-turnos-partner` (from `feature/entrega-2`).
Delivery strategy: ask-on-risk. Test runner: `go test ./...` plus `python scripts/check_turnos_contract.py`.

## Specs
- S1 — Group decision: any own service may be shared; Turnos is chosen and must be done properly. Scope: Turnos becomes the published capability. Partner routes in the Turnos service under `/v1`: `POST /v1/appointments`, `GET /v1/appointments/{id}`, `POST /v1/appointments/{id}/cancel`, `GET /v1/doctors`, `GET /v1/usage`; reachable through the gateway. Business rules unchanged (24 h lead time, no overlaps, enabled patient).
- S2 — Brief 4.1: "condiciones de **autenticación, idempotencia y uso**". Authentication: `X-API-Key` per consumer (401 `UNAUTHORIZED`); a consumer only sees and cancels appointments it created (others → 404 `NOT_FOUND`); per-consumer rate limit (429 `RATE_LIMITED`, `Retry-After`).
- S3 — Car Cruds (COMUNICACION-HOSPITAL.md): "Para recuperar creaciones inciertas sería necesario que ofrezca idempotencia o búsqueda por referencia". Scope: `Idempotency-Key` required on `POST /v1/appointments`; same key and same data → same appointment, **201** and `Idempotency-Replayed: true`; same key with other data → 409 `IDEMPOTENCY_CONFLICT`; concurrent requests with one key create one appointment. Rejected bookings (422/409/403/404) are not stored under the key.
- S4 — Group request: the shared service should charge the consumer for its use. Scope (assumption, pending group confirmation of the amount): charge per successful booking (not replays, not rejections, no refund on cancellation); unit price configured by `TURNOS_PARTNER_PRICE_CENTS` / `TURNOS_PARTNER_CURRENCY` and stored with each booking so price changes never rewrite history; `GET /v1/usage?period=YYYY-MM` returns the consumer's statement.
- S5 — Group request: document the capability well enough for the consumer group to integrate on its own. Scope: OpenAPI `docs/contracts/turnos-partner-v1.openapi.json`; `docs/contracts/README.md` rewritten as the consumer guide (base URL, headers, examples, errors, billing, migration notes for Car Cruds); ADR-008 records the change of published capability (Notificaciones → Turnos) keeping history; ADR-001, ADR-013, ENTREGA-2 and README updated. Notificaciones stays an internal service.
- S6 — Compatibility with the client Car Cruds already built (commit 9eec277): response fields `id, patientId, doctorId, startAt, endAt, status, version`; POST success is exactly 201; error codes `INVALID_REQUEST`/400, `INVALID_JSON`/400, `SLOT_UNAVAILABLE`/409, `PATIENT_NOT_ENABLED`/403, `UNKNOWN_DOCTOR`/404, `UNKNOWN_PATIENT`/404, `LEAD_TIME_TOO_SHORT`/422 unchanged. Only base URL and the two headers change for them.
- S7 — Brief 4.1: "Contrato **formal, versionado, estándar y procesable**". Scope: a runnable contract check against the running gateway, like `scripts/check_contract.py`.
- S8 — Group request (2026-10-23): close the unauthenticated `/api/turnos` exposure leaving it reachable only from the portal's internal network when the gateway is published). Scope: gateway second listener `PUBLIC_HTTP_ADDR` serving only `/v1/*` and `/healthz`; `HTTP_ADDR` stays internal with `/api/turnos/*` and `/metrics`.

## Tasks
| ID | Specs | Route | Status | Commit / evidence |
|---|---|---|---|---|
| T1 | S2 | inline | done | 35fe990 — apikey tests RED→GREEN; notifications tests unchanged and green |
| T2 | S1, S2, S3, S4, S6 | inline | done | 0c1a278 — app + HTTP tests RED→GREEN; MySQL integration green (12 concurrent same-key → 1 appointment, 1 charge) |
| T3 | S1 | inline | done | 34e48ca — gateway routing test RED→GREEN |
| T5 | S2, S8 | inline | done | gateway public listener — RED→GREEN; live 8088: /api/turnos and /metrics 404; check_turnos_contract.py 22/22 |
| T4 | S5, S7 | inline | done | 9f5f312 — check_turnos_contract.py 21/21 against compose stack |

T1 extract API key auth + rate limit from Notificaciones into `internal/platform/apikey` and reuse it. T2 Turnos partner API (schema, store, app, HTTP, config, tests). T3 gateway routes + compose env. T4 contract, contract check script, docs.
Route evidence: units are sequential (T2 depends on T1, T3/T4 on T2); no Writer trigger, parent writes inline.

## Log
- L1 (2026-10-23): The group asked what had been shared for Entrega 2. The documented shared capability was Notificaciones, while the group believed it should be Historia clínica; the consumer group's integration notes were unclear. Request: share the right service, document it for the consumer and charge for its use.
- L2 (2026-10-23): The consumer is Car Cruds (Gerpeirone/Car-Cruds, branch entrega-2/cierre); it may consume any service the group chooses to share. Request: find out what Car Cruds actually implemented.
- L3 (2026-10-23): Decision: share Turnos, done properly.
- L4: Car Cruds (entrega-2/cierre, 9eec277) consumes `POST/GET /api/turnos/appointments` observed from our code; their client requires 201 on POST and the error codes in S6; it tolerates extra response fields.
- L5: Docker Desktop was stopped; started it. `go test -tags integration ./services/turnos/internal/adapters/mysqlstore/` green (86 s).
- L6: Full stack with `TURNOS_PARTNER_KEYS=carcruds:…,otro-grupo:…`: `scripts/check_turnos_contract.py` 21/21 (auth, isolation, idempotency replay/conflict, business codes, usage, cancel without refund). Differential: `/api/turnos` unchanged (201 without key, 404 unknown); `scripts/check_contract.py` 18 passed.
- L7: Finding — internal `/api/turnos/*` stays unauthenticated (pre-existing, entrega-2 L11). Anyone reaching the gateway could book without key or billing; documented in ADR-008 as a deployment requirement; fix pending user decision.
- L8: `.env.example` still access-denied; new variables documented in docs/contracts/README.md and defaulted in compose.yaml.
- L9: Pending assumptions to confirm with Car Cruds: price ARS 25.000 per booking, no refund on cancel; real employees as patients (seed data only).
- L10: Independent verifier (item 2): S1–S7 PASS, no blockers. Probes: 16 concurrent same-key POSTs with 4 different payloads → 1 appointment (4×201 same id, 12×409), no deadlock; cross-consumer GET/cancel → 404; usage billed only created bookings. Advisories: /api/turnos unauthenticated (baseline); partner POST requires Content-Type (Car Cruds client already sets it, client.go:70); cancel ignores a body; duplicate keys or consumer names >64 chars not validated at startup; replay of a cancelled booking returns status cancelled. RDD: T2 0c1a278 high, consent declined by user (declined_this_candidate); 0c1a278..a99f70b medium/under_budget.
- L11 (2026-10-23): Approved closing the `/api/turnos` exposure.
- L12: Chose network separation (two listeners) over source-IP filtering because behind a tunnel or reverse proxy every request arrives from the proxy address; local 8080 behavior unchanged.
- L13: Independent verifier (S8): PASS, no blockers; path tricks (dot-segments, %2e%2e, %2f, absolute-form, Host) never reach internal routes on 8088; 8080 unchanged. Probes committed as TestPublicListenerResistsPathTricks. RDD: d99690e assessed high (escalated item 2), consent declined by user. Advisories: PUBLIC_HTTP_ADDR==HTTP_ADDR guard compares strings only (second bind still fails closed); shutdown of both listeners shares one 5 s budget.
