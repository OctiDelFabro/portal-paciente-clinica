# Entrega final — Presentación grupal (2026-11-11)
Objective: close every item the brief requires for the group presentation. Branch: `feature/entrega-final` (from `main` @ 9f0e5b2).
Delivery strategy: ask-on-risk (chained PRs if over budget). Test runner: `go test ./...` (`-tags integration` for real dependencies), `npm run build` in `frontend/`.

## Specs
Brief, section 6 "Presentacion Grupal" (quoted verbatim):
- S1 — "frontend completo, desde el ingreso del usuario hasta la funcionalidad compartida"
- S2 — "punto de entrada único con autenticación, direccionamiento y control de tráfico"
- S3 — "al menos tres servicios operativos"
- S4 — "implementación real de la capacidad propia, desplegada en la nube y accesible mediante una URL pública"
- S5 — "capacidad del proveedor incorporada al flujo principal" (2.4: called from the responsible microservice, not from the frontend or the gateway; consumer contract test, timeouts, error handling, user-visible behavior on provider failure)
- S6 — "al menos dos tipos de almacenamiento integrados al flujo" (2.8: "uno relacional, otra no relacional")
- S7 — "caché integrada en un flujo de lectura relevante, con medición comparativa antes y después"
- S8 — "evento de dominio publicado y consumido mediante mensajería, con consumidor idempotente y tratamiento de mensajes no procesables" (already done in Entrega 2; new consumers must keep it)
- S9 — "búsqueda indexada con paginación, filtros, ordenamiento y mecanismo de actualización o reconstrucción del índice"
- S10 — "verificación de las decisiones mediante la evidencia obtenida sobre el sistema"
- S11 — "sección de limitaciones conocidas y deuda técnica aceptada, actualizada en la descripción de la arquitectura (`docs/ARCHITECTURE.md`)"
- S12 — Balancing (2.3, ADR-012): Turnos in two or more instances, gateway round-robin among healthy ones, distribution visible in observability.
- S13 — Load tests (2.11, ADR-013) with documented results; k6 before/after cache (ADR-007).
- S14 — Provider: group "ArquitecturasoftwareRenewableEnergy", `POST /api/v1/estimaciones-consumo`, contract 1.0.1, `X-API-Key`. Returns kWh only; the cost in ARS is computed on our side with our own tariff. Consuming flow (L6): Turnos adds the energy cost of the consultation's equipment to the appointment price shown before confirming; on provider failure the booking proceeds with energy cost "pending".
- Assumption A1: portal identity is our own: Pacientes issues JWT (HS256, `JWT_SECRET` from env), the gateway validates it. Documented in a new ADR-014.
- Assumption A2: cloud target pending (Oracle E2.1.Micro or Azure for Students); fallback Cloudflare tunnel exposing only 8088.

## Tasks
| ID | Specs | Route | Status | Commit / evidence |
|---|---|---|---|---|
| T0 | — | inline | done | shared Go dependencies added to go.mod before parallel units |
| T1 | S3, S6, S9 | delegated (parallel) | done (integration not run) | 14f5bb7 — unit tests green; integration vetted only (Docker down) |
| T2 | S7, S12 | delegated (parallel) | done (integration not run) | 00c5dc3 — unit tests green; Redis/MySQL integration vetted only |
| T3 | S2, A1 | delegated (parallel) | done (integration not run) | 28d8d24 — unit tests green; MySQL integration vetted only |
| T4 | S2, S12, A1 | delegated (parallel) | done | 1c21a72 + fix 19fb425 — gateway tests green; verifier B1 fixed |
| T5 | S6, S8 | delegated (parallel) | done (integration not run) | 6a2d866 — unit tests green; Mongo/RabbitMQ integration vetted only |
| T6 | all | inline | done | compose stack up & healthy, full suite green, seam check passed end-to-end |
| T7 | S1 | inline | done | 68a953f — React/Vite patient portal (login, search, booking pass, history, agenda); build green |
| T8 | S5, S14 | delegated (parallel with T7) | done | 13e9592 — energy provider client + circuit breaker + quotes in Turnos + contract tests + provider stub; unit tests green |
| T9 | S7, S13 | inline | done | k6 availability (99.7% hit rate, 775µs p50) & booking peak (2916 reqs, 62ms p95, 409/429 validated); documented in ADR-007 and ADR-013 |
| T10 | S10, S11 | inline | pending | ADR-004, ARCHITECTURE limitations, VERIFICACION, POSTMORTEM |
| T11 | S4 | inline | blocked | cloud deploy (pending account) |
| T12 | S1, S2 | inline | done | doctor accounts (role doctor in Pacientes), doctor agenda in Turnos, doctor-to-appointment authorization in Historia, portal routes fail closed without X-Patient-Id; full suite & live e2e verified |

Route evidence: T1–T5 run as parallel writers (Writer trigger (a)): five independent units with disjoint edit surfaces under `services/<name>/**` and `gateway/**`, each several files and 100+ lines. Shared files (`compose.yaml`, `go.mod`, `docs/ARCHITECTURE.md`, `README.md`) are owned by the parent (T0, T6, T10).

## Log
- L1 (2026-11-08): The user asked to merge the Entrega 2 PR and then finish everything the brief requires for the group presentation on Wednesday, including designing the frontend. Price for partner bookings confirmed at ARS 25.000.
- L2: Gap analysis (explorer): done C8 (two patterns) and C9 (event, inbox, DLQ). Missing: frontend, portal auth and gateway rate limit, third real service, MongoDB/Redis/OpenSearch usage, provider integration, cloud deploy, balancing, k6, POSTMORTEM, ADR-004.
- L3: Provider repo reviewed (main @ 0ae9918, CI green): real Go implementation running locally, cloud URL pending. A message for the provider group was drafted (URL, M2M key, frozen v1, rate limit).
- L4: Cloud: Render Free sleeps after 15 min (about 1 min cold start) and has no free MySQL; Oracle ARM capacity unavailable for the user. Decision deferred by the user; code work continues.
- L5: Open decision: which microservice and business flow consumes the provider's energy estimate (S14).
- L6 (2026-11-08): Decision: appointment cost. When booking, Turnos asks the provider for the kWh of the equipment that consultation uses and adds the energy cost (kWh × own tariff) to the appointment price shown before confirming. If the provider fails, the booking still succeeds and the energy cost stays "pending" (controlled degradation). T8 starts after T2 to avoid editing services/turnos concurrently.
- L7: Parallel writers T1–T5 delivered; Docker unavailable, so every integration test is written and vetted but not run (pending T6).
- L8: Independent verifier on 1c21a72/28d8d24 (item 2): blocker B1, a client `Connection: X-Patient-Id` header made ReverseProxy drop the gateway-set identity, so a patient could act as any other. Fixed in 19fb425 with regression tests (RED observed, then GREEN); also future `iat` and unknown roles rejected. Advisories: Turnos/Pacientes fail open without X-Patient-Id (T12), rate-limit bucket map growth when full, register 409 reveals emails, login allowed for not-enabled accounts.
- L9: Frontend direction chosen by the user: boarding pass and live board (impeccable, Operate, code-led). PRODUCT.md and surface brief written. Doctor role needs backend support (T12).
- L10 (2026-11-08): T7 (frontend) committed in 68a953f; T8 (energy provider client, contract tests, quote and MySQL persistence in Turnos, provider-stub) committed in 13e9592. Next: T6 compose wiring & integration tests once Docker is running.
- L11 (2026-11-08): T6 completed. Docker stack up and healthy (gateway, turnos-1, turnos-2, pacientes, medicos, historia-clinica, notifications, provider-stub, mysql, mongodb, rabbitmq, opensearch, redis, prometheus, grafana, jaeger, mailpit). Seam check verified end-to-end: registration, JWT login, OpenSearch doctor search, Turnos quote with energy calculation from provider-stub, booking, RabbitMQ outbox publishing, Historia Clínica consumer in MongoDB, and timeline reading via Gateway.
- L12 (2026-11-08): T9 completed. Automated load tests executed via `grafana/k6` in Compose network (`infra/k6/availability.js` and `infra/k6/booking-peak.js`). Measured before/after cache at 200 req/s (99.74% hit rate in Redis, 775µs p50 vs 1.05ms without cache, 99.7% reduction in MySQL queries). Peak booking load test under contention validated MySQL row locks, 201 Created on winners, 409 Conflict on overlapping slots, and 429 Too Many Requests on partner rate limit. Evidence documented in ADR-007 and ADR-013.
- L13 (2026-11-08): T12 completed. Doctor accounts seeded (doc-001, doc-002) in Pacientes with role "doctor" and JWT issuance; Turnos added ListForDoctor/GetForDoctor/CancelForDoctor and closed fail-open advisory by requiring X-Patient-Id (401 on missing); Historia Clinica added doctor-to-appointment assignment authorization (403 if doctor != assigned doctor); Pacientes allows doctor role to read patient contacts for agenda; verified via unit tests, frontend build, and live Docker stack e2e execution.



