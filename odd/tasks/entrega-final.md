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
- S14 — Provider: group "ArquitecturasoftwareRenewableEnergy", `POST /api/v1/estimaciones-consumo`, contract 1.0.1, `X-API-Key`. Returns kWh only; the cost in ARS is computed on our side with our own tariff. Consuming flow: open decision (see Log).
- Assumption A1: portal identity is our own: Pacientes issues JWT (HS256, `JWT_SECRET` from env), the gateway validates it. Documented in a new ADR-014.
- Assumption A2: cloud target pending (Oracle E2.1.Micro or Azure for Students); fallback Cloudflare tunnel exposing only 8088.

## Tasks
| ID | Specs | Route | Status | Commit / evidence |
|---|---|---|---|---|
| T0 | — | inline | done | shared Go dependencies added to go.mod before parallel units |
| T1 | S3, S6, S9 | delegated (parallel) | pending | Médicos: MySQL + outbox + OpenSearch search + indexer + rebuild |
| T2 | S7, S12 | delegated (parallel) | pending | Turnos: availability endpoint + Redis cache-aside + authenticated patient + instance field |
| T3 | S2, A1 | delegated (parallel) | pending | Pacientes: register/login, bcrypt, JWT, MySQL |
| T4 | S2, S12, A1 | delegated (parallel) | pending | Gateway: JWT validation, rate limit, Turnos load balancing, new routes, ADR-014 |
| T5 | S6, S8 | delegated (parallel) | pending | Historia clínica: MongoDB + idempotent consumer of Turnos events + DLQ |
| T6 | all | inline | pending | compose.yaml wiring, go mod tidy, seam check (full suite + compose up) |
| T7 | S1 | inline | pending | Frontend: login → search → availability → booking → my appointments → history |
| T8 | S5, S14 | inline | pending | Provider integration + consumer contract test (needs flow decision) |
| T9 | S7, S13 | inline | pending | k6 load tests, before/after cache evidence, ADR-013 results |
| T10 | S10, S11 | inline | pending | ADR-004, ARCHITECTURE limitations, VERIFICACION, POSTMORTEM |
| T11 | S4 | inline | blocked | cloud deploy (pending account) |

Route evidence: T1–T5 run as parallel writers (Writer trigger (a)): five independent units with disjoint edit surfaces under `services/<name>/**` and `gateway/**`, each several files and 100+ lines. Shared files (`compose.yaml`, `go.mod`, `docs/ARCHITECTURE.md`, `README.md`) are owned by the parent (T0, T6, T10).

## Log
- L1 (2026-11-08): The user asked to merge the Entrega 2 PR and then finish everything the brief requires for the group presentation on Wednesday, including designing the frontend. Price for partner bookings confirmed at ARS 25.000.
- L2: Gap analysis (explorer): done C8 (two patterns) and C9 (event, inbox, DLQ). Missing: frontend, portal auth and gateway rate limit, third real service, MongoDB/Redis/OpenSearch usage, provider integration, cloud deploy, balancing, k6, POSTMORTEM, ADR-004.
- L3: Provider repo reviewed (main @ 0ae9918, CI green): real Go implementation running locally, cloud URL pending. A message for the provider group was drafted (URL, M2M key, frozen v1, rate limit).
- L4: Cloud: Render Free sleeps after 15 min (about 1 min cold start) and has no free MySQL; Oracle ARM capacity unavailable for the user. Decision deferred by the user; code work continues.
- L5: Open decision: which microservice and business flow consumes the provider's energy estimate (S14).
