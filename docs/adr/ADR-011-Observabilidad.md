# ADR-011 — Observabilidad
**Decisión D11 · Estado: primera versión, implementada parcialmente en la Entrega 2.** Logs JSON correlacionados, métricas Prometheus, trazas OpenTelemetry en Jaeger y un tablero Grafana. Alertas y agregación de logs quedan para la presentación final.

## Criterio
**Cada señal existe porque responde una pregunta concreta o dispara una decisión.** Si una métrica o un campo de log no cumple eso, no se guarda. También se descarta lo que expone datos de salud o genera series sin límite.

## Objetivos de servicio (SLO) propuestos
| SLO | Objetivo | Por qué |
|---|---|---|
| Disponibilidad de la API (gateway) | ≥ 99 % de solicitudes sin 5xx en 30 días | Un 5xx es una falla nuestra; 409/422 son rechazos de negocio correctos y no cuentan. |
| Latencia de reserva | p95 < 500 ms en `POST /appointments` | La reserva es la operación crítica y la que sufre picos de demanda. |
| Frescura de avisos | p95 < 60 s entre reserva y evento publicado | El paciente espera la confirmación; más de un minuto indica broker caído o relay detenido. |

Los valores son propuestas iniciales; D13 los valida con pruebas de carga.

## Métricas
| Métrica | Qué responde | Decisión o alerta que habilita |
|---|---|---|
| `http_requests_total{service,route,method,status}` | ¿Cuántas solicitudes fallan y dónde? Separa rechazos de negocio (409/422) de fallas (5xx). | SLO de disponibilidad. Alerta si 5xx > 2 % en 5 min. |
| `http_request_duration_seconds` (histograma) | ¿Cuánto tarda cada ruta en p95/p99? Los promedios esconden los picos. | SLO de latencia de reserva; insumo de D13. |
| `appointments_booked_total` · `appointments_rejected_total{reason}` | ¿Se reserva normalmente? ¿Por qué se rechaza? (`lead_time`, `overlap`, `patient_not_enabled`, `not_found`) | Un pico de `overlap` indica contención por demanda, la característica relevante del dominio. |
| `appointments_cancelled_total` | ¿Cuántas cancelaciones hay? | Contexto para interpretar la carga de Notificaciones. |
| `booking_lock_wait_seconds` | ¿Cuánto espera una reserva por el bloqueo del médico y del paciente? | Es el límite de capacidad esperado (D4, D13). Si sube con la carga, el cuello es la contención y no la CPU. |
| `outbox_pending` · `outbox_oldest_age_seconds` | ¿Se están publicando los eventos? | Si RabbitMQ cae, las reservas siguen pero los avisos se atrasan. Alerta si la antigüedad > 60 s. |
| `outbox_publish_total{result}` | ¿Las publicaciones se confirman, vuelven sin cola o fallan? | `returned` indica que Notificaciones no declaró su cola. |
| `events_consumed_total{result}` | ¿El consumidor procesa, deduplica, reintenta o descarta? | Muestra en vivo que la idempotencia y los reintentos funcionan. |
| `notifications_dlq_depth` | ¿Hay mensajes que requieren intervención humana? | Alerta si > 0: alguien tiene que inspeccionar y reprocesar. |
| `notifications_requests_total{consumer,template,outcome}` | ¿Cómo usa la API el grupo consumidor? | Soporte al otro grupo: ver rechazos, rate limit y uso por plantilla sin pedirles logs. |
| `idempotency_replays_total` · `idempotency_conflicts_total` | ¿El consumidor reintenta bien (replays) o reutiliza claves (conflicts)? | Detectar una integración incorrecta antes de que genere correos duplicados. |
| `email_send_duration_seconds` · `email_send_errors_total` · `email_circuit_open` | ¿El proveedor de correo responde? ¿Está abierto el circuito? | Es la dependencia externa más lenta: define el timeout de 5 s y el umbral del circuit breaker (D10). |
| `notifications_delivered_total{result}` | ¿Cuántos correos se envían, reintentan o fallan definitivamente? | `failed` creciente indica un problema persistente del proveedor. |
| `gateway_upstream_errors_total{upstream,kind}` · `gateway_retries_total{upstream}` | ¿Qué servicio interno falla o tarda, visto desde la entrada? | Ubicar la dependencia caída sin revisar cada servicio. |

**Etiquetas acotadas.** `route` es el patrón registrado (`GET /appointments/{id}`), nunca la URL real. `reason`, `result` y `outcome` tienen valores fijos. `consumer` solo toma los nombres configurados.

**Lo que no se guarda como métrica:** `patientId`, `doctorId`, correos, `appointmentId`. Generarían una serie por persona, Prometheus no escala con esa cardinalidad, y además serían datos de salud asociados a una persona.

**Previstas para la presentación final:** aciertos/fallos de caché (D7), retraso del índice de búsqueda (D6) y solicitudes por instancia (D12).

## Logs
Formato JSON a stdout con `log/slog` (`internal/platform/obs`). Un registro por hecho, no por línea de código.

| Campo o regla | Por qué |
|---|---|
| `time`, `level`, `msg` | Ordenar y filtrar. `msg` es legible por personas; para buscar se usa `event`. |
| `service`, `instance` | Saber quién escribió el registro. `instance` (hostname del contenedor) muestra el balanceo de D12. |
| `correlation_id` | Seguir una operación de negocio entre servicios, incluso a través de RabbitMQ y del trabajador de correo. Viene en `X-Correlation-ID` o se genera en el primer servicio. |
| `trace_id`, `span_id` | Saltar del log a la traza en Jaeger y viceversa. |
| `event` (nombre estable) | Buscar por hecho, no por texto: `appointment.booked`, `appointment.rejected`, `outbox.published`, `outbox.broker_unavailable`, `event.processed`, `event.duplicate`, `event.retry`, `message.dead_lettered`, `notification.accepted`, `notification.sent`, `notification.retry`, `notification.failed`, `gateway.upstream_timeout`. |
| `route`, `method`, `status`, `duration_ms` | Log de acceso por solicitud: diagnosticar un error sin reproducirlo. |
| `reason`, `attempt`, `retry`, `delay` | Explicar por qué se rechazó o reintentó algo. |
| IDs opacos (`appointment_id`, `notification_id`, `event_id`, `doctor_id`) | Rastrear un caso puntual. En logs sí: no generan cardinalidad, como ocurriría en una métrica. |
| `error` | Mensaje técnico de la causa; nunca incluye el cuerpo de la solicitud. |

**Niveles.** INFO para hechos de negocio esperados. WARN para rechazos, reintentos y degradaciones que el sistema maneja solo. ERROR para fallas terminales o mensajes en DLQ, que siempre requieren acción. Si un ERROR no requiere acción, está mal clasificado.

**Nunca se registran:** correos, nombres, contenido clínico, `X-API-Key`, contraseñas, DSN ni cuerpos de solicitud. Son datos personales y de salud; ADR-008 ya exigía no registrar claves.

## Trazas
OpenTelemetry con propagación W3C `traceparent`, exportada por OTLP/HTTP a Jaeger. Traza del flujo principal:

`gateway (servidor)` → `gateway → turnos (cliente)` → `turnos POST /appointments` → *outbox* → `publish turnos.reservado.v1` → `consume turnos.reservado.v1` (notifications) → `send email`

El contexto de traza se guarda junto con el evento en la outbox y en la fila de la notificación. Por eso la traza continúa aunque el relay y el trabajador de correo sean procesos asíncronos que corren más tarde. `/healthz` y `/metrics` se excluyen para no llenar Jaeger de ruido.

## Tablero y alertas
Grafana provisionado (`infra/grafana/dashboards/portal-overview.json`) con un panel por pregunta de la tabla de métricas.

Alertas propuestas para la presentación final:
- 5xx > 2 % durante 5 min.
- p95 de reserva > 500 ms durante 10 min.
- `outbox_oldest_age_seconds` > 60.
- `notifications_dlq_depth` > 0.
- `email_circuit_open` = 1 durante más de 2 min.

## Alternativas consideradas
- **Logs de texto libre:** más simples, pero no se pueden filtrar por `correlation_id` ni contar eventos.
- **Solo logs, sin métricas:** obliga a contar líneas para ver tasas y percentiles; es caro y lento.
- **Etiquetar métricas por paciente:** se descarta por cardinalidad y privacidad; los casos puntuales se buscan en logs y trazas.

## Pendientes
Agregación de logs (Loki o equivalente), reglas de alerta en Prometheus/Grafana y métricas de D6, D7 y D12.
