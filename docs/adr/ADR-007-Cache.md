# ADR-007 — Caché
**Decisión D7 · Estado: aceptada e implementada en la entrega final; medición k6 pendiente (D13).** Redis cachea la disponibilidad de un médico por día, leída en Turnos. Vigencia de 30 s e invalidación explícita al reservar o cancelar.

## Contexto
La característica relevante del dominio son los **picos de demanda**: cuando se abre una agenda, muchos pacientes consultan la disponibilidad del mismo médico a la vez. Calcular la disponibilidad requiere leer la configuración de agenda y los turnos activos del día. Es la lectura más repetida y más costosa del flujo principal, y compite por MySQL con las reservas, que sí necesitan bloqueos (D4).

## Decisión
| Aspecto | Decisión |
|---|---|
| Qué se guarda | Lista de franjas libres de `doctorId` + `fecha`. Clave `availability:v1:{doctorId}:{yyyy-mm-dd}`. Sin datos de pacientes. |
| Vigencia | TTL de 30 s, como red de seguridad si se pierde una invalidación. |
| Invalidación | Turnos borra la clave del día afectado después del commit de una reserva, cancelación o bloqueo. |
| Patrón | Cache-aside en el adaptador de lectura de Turnos. |
| Autoridad | La caché **nunca** confirma una reserva: `POST /appointments` siempre valida bajo bloqueo en MySQL (D3/D4). Una franja mostrada como libre puede estar ocupada; el resultado es 409 `SLOT_UNAVAILABLE`. |
| Falla de Redis | Se lee MySQL directamente con timeout de 100 ms hacia Redis, log WARN `cache.unavailable` y métrica. El portal se degrada en latencia, no en función. |

## Medición
- Métricas: `cache_requests_total{result="hit|miss|error"}` y la latencia de `GET /doctors/{id}/availability`.
- Evidencia exigida: misma prueba de carga k6 (D13) con la caché desactivada y activada, comparando p95, consultas por segundo a MySQL y `booking_lock_wait_seconds`.
- Se espera una tasa de aciertos > 80 % en el escenario de pico. Si no se alcanza, la decisión se revisa en un nuevo ADR.

## Alternativas consideradas
- **Cachear perfiles de médicos:** cambian poco, pero OpenSearch ya responde rápido (D6); el beneficio sería marginal.
- **Cachear la historia clínica:** se descarta por privacidad y porque los permisos dependen de relaciones que cambian (D1, RF-10).
- **Caché en memoria por instancia:** no requiere Redis, pero con varias réplicas (D12) cada instancia tendría una vista distinta y la invalidación no llegaría a todas.

## Consecuencias
Se acepta mostrar una disponibilidad desactualizada hasta 30 s en el peor caso (invalidación perdida), a cambio de proteger MySQL en los picos. Se agrega Redis como dependencia no crítica.

## Implementación
| Aspecto | Implementación | Evidencia |
|---|---|---|
| Lectura | `GET /doctors/{id}/availability?date=AAAA-MM-DD` devuelve las franjas libres del día (hora de la clínica, UTC-03:00), listas para enviar como `startAt` de una reserva. | `httpapi/availability.go`, `httpapi/availability_test.go` |
| Cálculo | `domain.FreeSlots` divide la agenda semanal del médico según su duración de turno y descarta franjas con menos de 24 h de anticipación o superpuestas con turnos activos del médico: las mismas reglas que `domain.Book`. | `domain/availability_test.go` (incluye "toda franja libre es reservable") |
| Agenda | Configuración mínima en el directorio semilla: `doc-001` lun–vie 09:00–13:00 (30 min); `doc-002` lun–vie 14:00–18:00 (20 min). | `directory/seed.go` |
| Cache-aside | Adaptador `rediscache` que decora la consulta a MySQL: clave `availability:v1:{doctorId}:{yyyy-mm-dd}`, TTL 30 s, timeout de 100 ms por operación y sin reintentos. | `rediscache/cache.go`, `cache_integration_test.go` |
| Invalidación | `app.Service` borra la clave del día de inicio (y del día de fin, si difiere) después del commit de reservas y cancelaciones, del portal y de la API de socios. Una repetición idempotente no invalida. | `app/availability_test.go` |
| Falla de Redis | Se lee MySQL, log WARN `cache.unavailable` (campo `op`: get, set o invalidate) y `cache_requests_total{result="error"}`. | `rediscache/cache_test.go` (Redis colgado: respuesta de MySQL en menos de 600 ms) |
| Comparación | `CACHE_ENABLED=false` lee siempre MySQL, para la línea base de k6; `REDIS_ADDR` (por defecto `127.0.0.1:6379`). | `cmd/main.go` |
| Latencia | Se usa `http_request_duration_seconds{service="turnos",route="GET /doctors/{id}/availability"}` del middleware común; no se agregó un histograma propio. | `internal/platform/obs/http.go` |

### Desvíos y limitaciones
- La invalidación por bloqueo de agenda (RF-07) no aplica todavía: Turnos no implementa bloqueos.
- La agenda no se valida al reservar: `POST /appointments` mantiene sus reglas (24 h y superposición) para no cambiar el contrato vigente. Una franja fuera de agenda sigue siendo reservable por la API.
- Carrera conocida del patrón: una lectura que empezó antes del commit puede guardar un valor viejo después de la invalidación. Lo acota el TTL de 30 s; la reserva sigue validándose bajo bloqueo en MySQL y responde 409 `SLOT_UNAVAILABLE`.
- Un valor cacheado se calculó con la hora de ese momento: una franja que deja de cumplir las 24 h puede mostrarse hasta 30 s más y la reserva responde 422 `LEAD_TIME_TOO_SHORT`.
- Pendiente: resultados k6 con caché desactivada y activada, tasa de aciertos y justificación de `cache_requests_total` en ADR-011.
