# ADR-007 — Caché
**Decisión D7 · Estado: aceptada; implementación y medición previstas para la presentación final.** Redis cachea la disponibilidad de un médico por día, leída en Turnos. Vigencia de 30 s e invalidación explícita al reservar o cancelar.

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
