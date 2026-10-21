# Estado de la Entrega 2
Referencia: enunciado, sección 6.3, entrega del viernes 23 de octubre: "Dominio propio de punta a punta".

**Resumen:** la reserva de turnos funciona de punta a punta. Turnos guarda la reserva en MySQL y publica el evento por outbox; Notificaciones lo consume en forma idempotente y envía el correo (capturado por Mailpit). Todo el recorrido queda en una traza de Jaeger y en logs con el mismo `correlation_id`.

## Exigencias
| Exigencia | Estado | Dónde verlo |
|---|---|---|
| Al menos un servicio operativo | **Turnos**: reserva, consulta y cancelación con las reglas de RF-02/RF-03 | `services/turnos`, README → Puesta en marcha |
| Funcionalidad compartida funcionando | **Notificaciones real** reemplaza al mock con el mismo contrato (1.1.0, compatible) | `services/notifications`, `docs/contracts/README.md` |
| Almacenamiento integrado al flujo | **MySQL** en Turnos (turnos, bloqueos, outbox) y en Notificaciones (solicitudes, idempotencia, inbox, avisos) | ADR-003 → Validación |
| Logs estructurados y correlacionados | JSON con `correlation_id`, `trace_id`, `event` | ADR-011 → Logs |
| Primera traza distribuida del flujo principal | gateway → turnos → publish → consume → send email | Jaeger, ADR-011 → Trazas |
| D2, D6, D7, D9, D10, D12, D13 | Registradas | `docs/adr/` |
| Validación de D1, D3, D5 | Sección "Validación Entrega 2" en cada ADR | `docs/adr/` |
| Primera versión de D11 | Métricas y logs **justificados uno por uno** (pedido de la cátedra) | ADR-011 |
| Actualización de D8 | Contrato 1.1.0: cambio compatible en `/healthz` | ADR-008 → Actualización |

## Bonus anunciados
Se anuncian dos desafíos para incorporar al alcance (pendiente de confirmación del grupo):
1. **Tests e2e:** recorrido automatizado frontend → gateway → servicios → Mailpit.
2. **Actualizaciones en tiempo real:** avisos del portal (`portal_notices`) enviados al frontend por Server-Sent Events.

## Demostración sugerida (10 minutos)
1. `docker compose up --build`.
2. Reservar `pat-001` con `doc-001` (README). Mostrar el correo en Mailpit y la traza en Jaeger.
3. Repetir la misma reserva → 409 `SLOT_UNAVAILABLE`. Reservar con menos de 24 h → 422 `LEAD_TIME_TOO_SHORT`. Mostrar los rechazos por motivo en Grafana.
4. `docker compose stop rabbitmq`, reservar otro turno: la reserva responde 201 y `outbox_oldest_age_seconds` crece. `docker compose start rabbitmq`: el correo llega.
5. Mostrar en logs el mismo `correlation_id` en gateway, turnos y notifications.

## Limitaciones conocidas
- Pacientes y Médicos no están operativos: Turnos y Notificaciones usan datos semilla a través de puertos (`directory`, `contacts`).
- Sin autenticación de usuarios en el gateway ni frontend integrado (presentación final).
- Sin agenda configurable ni bloqueos (RF-06/RF-07), reprogramación ni recordatorios.
- D6, D7, D12 y D13 están decididos pero no implementados ni medidos.
- El rate limit de la API pública es por instancia.
- Los tests de integración (`-tags integration`) y `docker compose up` **no se ejecutaron en la sesión de desarrollo** porque Docker no estaba disponible. Hay que correrlos antes de presentar.
- `scripts/check_contract.py` falla de forma intermitente en Windows en el chequeo "limite de cuerpo" (el servidor corta la conexión antes de que llegue el 413). Ya ocurría en la Entrega 1; pendiente de corregir.

## Pendiente para la presentación final
Pacientes y Médicos reales, búsqueda (D6), caché con medición (D7), balanceo de Turnos (D12), pruebas de carga (D13), proveedor externo (D9), alertas, despliegue público de Notificaciones y `docs/POSTMORTEM.md`.
