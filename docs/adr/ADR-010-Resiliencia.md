# ADR-010 — Resiliencia
**Decisión D10 · Estado: aceptada; mecanismos principales implementados en la Entrega 2.** Cada dependencia tiene un comportamiento definido ante falla o lentitud. Ninguna falla de un aviso revierte una decisión de negocio, y ninguna falla de validación permite reservar sin validar.

## Dependencias y comportamiento
| Servicio → dependencia | Si falla o está lenta | Mecanismo | Estado |
|---|---|---|---|
| Gateway → Turnos / Notificaciones | 504 `UPSTREAM_TIMEOUT` (resultado indeterminado) o 502 `UPSTREAM_UNAVAILABLE` | Conexión 500 ms, respuesta 2 s, presupuesto total 5 s; un reintento solo para GET | Implementado |
| Turnos → MySQL | No se reserva ni cancela: 500/503. `/healthz` devuelve 503 | Reintento automático ante deadlock (hasta 3); espera de arranque | Implementado |
| Turnos → RabbitMQ | Las reservas siguen; los eventos esperan en la outbox y se publican al volver el broker | Outbox transaccional + publisher confirms + `mandatory` | Implementado |
| Notificaciones → MySQL (API pública) | 503 `SERVICE_UNAVAILABLE`; el consumidor reintenta con la misma clave | Registro durable antes del 202 | Implementado |
| Notificaciones → MySQL (eventos) | El evento se reintenta a los 5 s, 30 s y 120 s; después, DLQ | Colas de espera con TTL + DLX | Implementado |
| Notificaciones → evento mal formado | Va directo a la DLQ, sin reintentos inútiles | Clasificación `ErrUnprocessable` | Implementado |
| Notificaciones → proveedor de correo | Reintentos a los 5/30/120 s y después `failed`. Tras 5 fallas seguidas se abre el circuito 30 s y no se consumen intentos | Timeout 5 s + backoff + circuit breaker | Implementado |
| Notificaciones → RabbitMQ | Reconexión cada 3 s; los eventos se acumulan en la cola o en la outbox de Turnos | Bucle de reconexión | Implementado |
| Turnos → Pacientes / Médicos | Sin validación no hay reserva: 503 con mensaje claro | Timeout 2 s, sin reintento de escrituras | Pendiente (hoy adaptador semilla) |
| Turnos → Redis (D7) | Lectura directa de MySQL, mayor latencia | Timeout 100 ms + fallback | Pendiente |
| Médicos → OpenSearch (D6) | Búsqueda 503; el resto del portal sigue | Timeout + mensaje | Pendiente |

## Principios
- **No perder:** todo evento nace en la misma transacción que el cambio (outbox) y se reconoce solo después de persistirlo (inbox). Ante la duda se prefiere un duplicado, que absorbe la idempotencia, a una pérdida.
- **No duplicar efectos:** índice único por `eventId`, idempotencia HTTP por consumidor y clave, versión del turno.
- **No reintentar escrituras sin idempotencia:** una respuesta ausente no prueba que la escritura falló (D5).
- **Degradar, no caer:** los avisos pueden atrasarse; las reservas no se detienen por un problema de correo o de broker.

## Caída provocada (para `docs/POSTMORTEM.md`)
Ensayo planificado para la jornada de fallas:
1. Generar reservas con k6 a carga constante.
2. Detener RabbitMQ durante 2 minutos.
3. Observar en Grafana: las reservas continúan, `outbox_oldest_age_seconds` crece y se dispara la alerta de 60 s.
4. Levantar RabbitMQ: la outbox se vacía y los correos llegan a Mailpit, sin duplicados (`events_consumed_total{result="duplicate"}`).
5. Segundo ensayo: detener Mailpit para abrir el circuito (`email_circuit_open = 1`) y verificar la recuperación.

## Alternativas consideradas
- **Publicar directo a RabbitMQ en la solicitud:** si el broker cae, la reserva falla o el evento se pierde. La outbox desacopla ambas cosas.
- **Reencolar inmediatamente (`requeue=true`):** genera un bucle caliente que satura al consumidor; se usan esperas crecientes.
- **Reintentos infinitos de correo:** ocultan una caída del proveedor; con cuatro intentos el estado `failed` queda visible para el consumidor.
