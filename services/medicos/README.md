# Médicos

Owns doctor profiles and specialties. MySQL is the source of truth. OpenSearch is a derived search index kept in sync through an outbox and RabbitMQ. Design and deviations: [ADR-006](../../docs/adr/ADR-006-Busqueda.md).

## Layout

| Package | Role |
|---|---|
| `internal/domain` | Pure rules: profile validation, search limits, `NextFree`. |
| `internal/app` | Use cases and ports (`Store`, `Tx`, `Index`). |
| `internal/adapters/mysqlstore` | Schema, idempotent seed, profile and booking projection. |
| `internal/adapters/outbox` | Relay from the `outbox` table to RabbitMQ, a copy of the Turnos relay. |
| `internal/adapters/opensearch` | REST client: search, versioned upsert, rebuild with alias swap. |
| `internal/adapters/indexer` | AMQP consumer with retry queues and a DLQ. |
| `internal/adapters/httpapi` | HTTP endpoints. |

## HTTP API (port 8083; the gateway exposes it as `/api/medicos/*`)

| Method and path | Access | Result |
|---|---|---|
| `GET /search?q=&specialty=&acceptsMinors=&sort=name\|nextAvailable&page=&size=` | public | `{"items":[...],"page":1,"size":10,"total":N}`. `size` ≤ 50 and `page*size` ≤ 1000, otherwise 422. Returns 503 `SEARCH_UNAVAILABLE` if OpenSearch is down. |
| `GET /doctors/{id}` | public | Profile read from MySQL. Works while OpenSearch is down. |
| `GET /specialties` | public | `{"specialties":[{"code","name"}]}` |
| `PUT /doctors/{id}` | `X-User-Role: admin` | Body `{"fullName","specialties":[codes],"acceptsMinors","active"}`, all required. Saves the change and emits `medicos.perfil-actualizado.v1` in the same transaction. |
| `POST /admin/reindex` | `X-User-Role: admin` | Builds `doctors-vN+1` from MySQL and swaps the `doctors` alias. Returns `{"index","documents"}`. |
| `GET /healthz` | public | 503 only if MySQL is down. The `search` field reports OpenSearch separately. |
| `GET /metrics` | internal | Prometheus metrics. |

Errors use the Turnos shape: `{"error":{"code","message"}}`. Without the admin role the protected endpoints return 403 `FORBIDDEN`. The gateway sets `X-User-Role` from a validated JWT and strips any copy sent by the client.

You can also rebuild the index from the command line: `go run ./services/medicos/cmd reindex`, using the same environment variables.

## Configuration

| Variable | Example | Default |
|---|---|---|
| `MEDICOS_DB_DSN` (required) | `medicos:secret@tcp(mysql:3306)/medicos` | none |
| `RABBITMQ_URL` | `amqp://localuser:secret@rabbitmq:5672/` | `amqp://guest:guest@127.0.0.1:5672/` |
| `OPENSEARCH_URL` | `http://opensearch:9200` | `http://127.0.0.1:9200` |
| `HTTP_ADDR` | `0.0.0.0:8083` | `127.0.0.1:8083` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://jaeger:4318` | tracing as in the other services |

The schema and the seed (`doc-001` to `doc-004`, the same IDs whose agendas Turnos knows) are applied on every start. Rows that already exist are left as they are.

## Messaging

- Publishes `medicos.perfil-actualizado.v1` to the `portal.events.v1` exchange.
- The queue `medicos.indexer.v1` is bound to `medicos.perfil-actualizado.v1`, `turnos.reservado.v1` and `turnos.cancelado.v1`.
- Retry queues are `.retry.5s`, `.retry.30s` and `.retry.120s`. Messages that cannot be processed, or that run out of retries, go to `medicos.indexer.v1.dlq` through `portal.events.dlx`.

## Metrics

| Metric | Meaning |
|---|---|
| `search_index_lag_seconds` | Time from the event's `occurredAt` until the document is indexed. ADR-006 target: p95 ≤ 5 s. |
| `events_consumed_total{result}` | Indexer results: `ok`, `duplicate`, `retry` or `dlq`. |
| `medicos_indexer_dlq_depth` | Messages waiting in the indexer DLQ. |
| `outbox_pending`, `outbox_oldest_age_seconds`, `outbox_publish_total{result}` | Outbox relay state. |
| `http_*` | Request metrics from the shared middleware. |

## Tests

```sh
go test ./services/medicos/...                     # unit tests, using fakes and httptest
go test -tags integration ./services/medicos/...   # real MySQL, OpenSearch and RabbitMQ (Docker)
```
