# ADR-008 — Contrato propio: capacidad publicada de Turnos
**Decisión D8 · Estado: vigente desde el 23/10/2026, contrato 1.0.0 operativo en local.** Reemplaza la decisión anterior (Notificaciones), que se conserva abajo como historia.

## Contexto
La cátedra asignó a **Car Cruds** (concesionaria) como consumidor y aclaró que puede consumirse cualquier servicio propio. Car Cruds necesita que sus empleados y candidatos se hagan chequeos en la clínica antes de trabajar: su servicio Empleados ya había implementado la reserva y consulta de turnos leyendo nuestra ruta interna `/api/turnos` (repositorio `Gerpeirone/Car-Cruds`, rama `entrega-2/cierre`, commit `9eec277`, documento `COMUNICACION-HOSPITAL.md`). Señalaron como faltantes: autenticación, contrato formal, idempotencia de creación y tarifas. La API de Notificaciones que publicábamos no les servía: un correo no reemplaza una reserva.

## Decisión
Turnos es la capacidad publicada. Contrato OpenAPI 3.1.1 en [turnos-partner-v1.openapi.json](../contracts/turnos-partner-v1.openapi.json), API 1.0.0, guía en [contracts/README.md](../contracts/README.md):
- `GET /v1/doctors`, `POST /v1/appointments`, `GET /v1/appointments/{id}`, `POST /v1/appointments/{id}/cancel`, `GET /v1/usage`.
- `X-API-Key` por consumidor (`TURNOS_PARTNER_KEYS`). Cada consumidor solo ve y cancela sus turnos; uno ajeno responde 404, igual que uno inexistente.
- `Idempotency-Key` obligatoria al reservar, **permanente** y por consumidor. Misma clave y mismos datos: el mismo turno con 201 e `Idempotency-Replayed: true`. Otros datos: 409 `IDEMPOTENCY_CONFLICT`. Los rechazos de negocio no quedan asociados a la clave.
- Facturación por reserva creada, con el precio vigente guardado en cada reserva; la cancelación no reintegra. `GET /v1/usage` devuelve el estado de cuenta mensual.
- 60 solicitudes por minuto por consumidor; 429 con `Retry-After`.

La implementación vive en el propio servicio Turnos (adaptador `httpapi/partner.go`), no en el gateway: el dueño del recurso decide quién puede verlo y cobrarlo. El gateway solo enruta `/v1/appointments`, `/v1/doctors` y `/v1/usage` hacia Turnos. La autenticación y el límite se extrajeron a `internal/platform/apikey` y los usan Turnos y Notificaciones.

## Justificación
**Por qué Turnos y no otro servicio.** Es el único servicio operativo con reglas de negocio reales (anticipación, superposición, habilitación) y es lo que el consumidor ya necesita: su flujo de chequeos de personal es reservar y seguir un turno. Publicar Historia clínica obligaría a construir el servicio completo y a exponer datos sensibles que el consumidor declaró no querer ("no se almacena diagnóstico"). Publicar Turnos le permite migrar cambiando la URL base y dos headers, porque se mantuvieron los campos de respuesta, el 201 y los códigos de rechazo que su adaptador ya interpreta.

**Idempotencia permanente, no por ventana.** Una reserva ocupa un horario y se cobra: repetirla días después no debe crear otra. En Notificaciones la ventana de 24 h alcanzaba porque un correo duplicado es una molestia; un turno y un cargo duplicados son un error de negocio. La clave se bloquea junto con médico y paciente mediante el mismo `LockResources` en orden estable (ADR-003), así que no se agrega otro mecanismo de concurrencia ni otra causa de deadlock; además, `UNIQUE (consumer, idem_key)` en `partner_bookings` lo garantiza en la base.

**Replay con 201.** El cliente de Car Cruds trata como éxito solamente el 201. Devolver 200 en la repetición lo haría marcar como "desconocido" un turno que existe. La diferencia se informa en `Idempotency-Replayed`.

**Cobro por reserva creada, sin reintegro.** Es auditable (un registro por cargo, conciliable con `items`), estable (un estado de cuenta cerrado no cambia si después se cancela) y desalienta reservar y cancelar en masa. El precio se congela en cada reserva para que un cambio de tarifa no reescriba la historia. Montos en centavos enteros, sin decimales binarios.

## Alternativas consideradas
- **Historia clínica:** descartada por lo explicado arriba; podría sumarse luego un resultado de chequeo con consentimiento del paciente, como capacidad aparte.
- **Seguir con Notificaciones:** no cubre el flujo del consumidor.
- **Cobro por solicitud HTTP:** penaliza reintentos y consultas, que son justamente el comportamiento correcto ante fallas.
- **Reintegro al cancelar:** el estado de cuenta de un mes cambiaría después de cerrado; se puede revisar con el consumidor.
- **Autenticación en el gateway:** obligaría al gateway a conocer la propiedad de los turnos; el servicio dueño ya la tiene en su base.

## Compatibilidad y publicación
`/v1` es la versión mayor; cambios incompatibles exigen `/v2` y guía de migración. La URL pública se entrega a cada consumidor con su clave. La ruta interna `/api/turnos` **no** es parte del contrato y no tiene autenticación: en un despliegue compartido no debe quedar expuesta fuera de la red del portal.

## Consecuencias y límites
- Nueva tabla `partner_bookings` (dueño, clave, huella, precio, fecha). No modifica `appointments`.
- Una reserva de un socio publica el mismo evento `turnos.reservado.v1`; Notificaciones envía el correo al paciente como en cualquier reserva.
- Médicos y pacientes siguen siendo datos semilla: el alta de empleados reales como pacientes queda pendiente del servicio Pacientes.
- Sin reprogramación ni consulta de disponibilidad en v1.
- El límite de solicitudes es por instancia.
- Verificación: tests unitarios de app y HTTP, test de integración con MySQL real (12 solicitudes concurrentes con la misma clave crean un turno y un cargo) y `scripts/check_turnos_contract.py` contra el stack completo.

## Historial — decisión anterior: capacidad de Notificaciones (Entrega 1 y Entrega 2, hasta el 22/10/2026)
La API de Notificaciones sigue funcionando con su contrato; ya no es la capacidad publicada. Guía: [NOTIFICATIONS.md](../contracts/NOTIFICATIONS.md).

**Decisión D8 · Estado: contrato inicial 1.0.0 y mock preparados.** La capacidad operativa pública permanece pendiente; el contrato inicial será validado con el grupo consumidor cuando sea asignado.

### Contexto
Se ofrece correo electrónico mediante plantillas generales de confirmación, cancelación y recordatorio. El portal utiliza también avisos internos, que no se exponen al otro grupo. El dominio del consumidor no se conoce. El grupo confirmó X-API-Key distinta por consumidor.

### Decisión
Contrato OpenAPI 3.1.1 en ../contracts/notifications-v1.openapi.json, API 1.0.0 con rutas /v1:
- GET /v1/templates.
- POST /v1/notifications.
- GET /v1/notifications/{id}.

La petición identifica plantilla, destinatario y parámetros. El POST válido devuelve 202, id, accepted y Location. El envío será asíncrono; GET devuelve accepted, sent o failed. sent no prueba entrega ni lectura. Los errores y ejemplos están en ../contracts/NOTIFICATIONS.md.

Idempotency-Key por consumidor, ventana inicial de 24 h, normalización del orden de propiedades JSON. Misma clave y datos devuelve el mismo ID y la respuesta original; otros datos devuelve 409. Consultar solicitudes ajenas devuelve 404. 60 solicitudes por minuto por consumidor, cuerpo máximo 16 KiB. Estos límites son parámetros técnicos iniciales, sujetos a pruebas.

Las plantillas usan identificadores estables con sufijo .v1 y parámetros documentados. El consumidor no envía texto libre. El recordatorio externo no programa envíos futuros: se solicita al momento elegido por el consumidor. El recordatorio interno de un turno pertenece a Turnos, 24 h antes, con invalidación de versiones obsoletas.

RabbitMQ se utiliza para eventos internos; su topología y credenciales no forman parte del contrato compartido. Si falla el correo de una cancelación por bloqueo, el turno sigue cancelado; se persiste el aviso interno y se reintenta el correo. Los avisos de menores se dirigen al menor y responsable; sin correo propio del menor, el correo va solo al responsable.

### Justificación
HTTP/OpenAPI permite describir operaciones, entrada, resultado y errores sin acceso al broker. La clave por grupo facilita identificación y aislamiento con poca configuración para este TP. HTTPS, configuración externa y ausencia de claves en logs son necesarias para el servicio real.
202 distingue aceptación de resultado. Idempotencia permite repetir solicitudes cuya respuesta se perdió, sin crear deliberadamente otro correo. El consumidor consulta estados sin depender de detalles internos.

### Alternativas consideradas
RabbitMQ para el otro grupo exige acuerdos de broker, colas y operación compartida. JWT requiere un emisor y configuración adicional; podrá evaluarse posteriormente. HTTP para eventos internos obliga a resolver recuperación de mensajes; se conserva RabbitMQ. Texto libre se excluye por decisión funcional del grupo.

### Compatibilidad y publicación
/v1 es la versión mayor; cambios incompatibles en rutas, campos obligatorios, plantillas o significado exigen /v2 y guía de migración. Extensiones compatibles pueden mantener v1. Se conservarán contratos y ADR históricos.
El consumidor usará la URL publicada desde su microservicio responsable. La publicación pública, los responsables de acceso, proveedor de correo y funcionamiento durable están pendientes del siguiente hito; no hay URL productiva acreditada.

### Consecuencias y límites
Se requiere almacenamiento durable antes de 202, control concurrente de claves, reintentos, privacidad y observabilidad. La ventana de 24 h no deduplica indefinidamente. La idempotencia HTTP no garantiza entrega exactamente una vez por un proveedor externo.
El mock usa Go y memoria, valida solicitudes, aísla consumidores y simula resultados. Reiniciarlo pierde sus datos. No envía correos, no publica eventos ni demuestra la persistencia real.

### Referencias
- [OpenAPI 3.1.1](https://spec.openapis.org/oas/v3.1.1.html).
- [HTTP 202](https://www.rfc-editor.org/rfc/rfc9110.html#name-202-accepted).
- [RabbitMQ: confirms y acknowledgements](https://www.rabbitmq.com/docs/confirms).

### Actualización Entrega 2 — contrato 1.1.0
**Cambio compatible.** El servicio real reemplaza al mock como implementación principal y el contrato pasa de 1.0.0 a **1.1.0**, dentro de `/v1`:
- `GET /healthz`: `mode` admite `mock` o `service` y `simulated` pasa a booleano (antes eran constantes del mock). Se documenta el 503 cuando el servicio no tiene almacenamiento.
- `servers`: se agrega el gateway local (`http://localhost:8080`) para el servicio real. El mock sigue disponible con `docker compose --profile mock up` en el puerto 8086.
- Rutas, campos, plantillas, errores e idempotencia no cambian. Las respuestas del servicio real tienen `simulated: false`.

Por qué es compatible: ningún campo obligatorio se agregó ni cambió de significado para las operaciones de correo. Un consumidor que solo use `/v1/templates` y `/v1/notifications` no necesita cambios. Un cliente que comparaba `mode == "mock"` debe aceptar `service`.

Validación del comportamiento real: aceptación durable antes del 202, 30 POST concurrentes con la misma clave crean una sola notificación, clave reutilizada con otros datos da 409, 503 si la base no está disponible. El rate limit sigue siendo de 60/min por consumidor y **por instancia**.

Pendiente: URL pública desplegada y proveedor de correo real; en local los correos quedan en Mailpit.
