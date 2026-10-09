# ADR-008 — Contrato propio de Notificaciones
**Decisión D8 · Estado: contrato inicial 1.0.0 y mock preparados.** La capacidad operativa pública permanece pendiente; el contrato inicial será validado con el grupo consumidor cuando sea asignado.

## Contexto
Se ofrece correo electrónico mediante plantillas generales de confirmación, cancelación y recordatorio. El portal utiliza también avisos internos, que no se exponen al otro grupo. El dominio del consumidor no se conoce. El grupo confirmó X-API-Key distinta por consumidor.

## Decisión
Contrato OpenAPI 3.1.1 en ../contracts/notifications-v1.openapi.json, API 1.0.0 con rutas /v1:
- GET /v1/templates.
- POST /v1/notifications.
- GET /v1/notifications/{id}.

La petición identifica plantilla, destinatario y parámetros. El POST válido devuelve 202, id, accepted y Location. El envío será asíncrono; GET devuelve accepted, sent o failed. sent no prueba entrega ni lectura. Los errores y ejemplos están en ../contracts/README.md.

Idempotency-Key por consumidor, ventana inicial de 24 h, normalización del orden de propiedades JSON. Misma clave y datos devuelve el mismo ID y la respuesta original; otros datos devuelve 409. Consultar solicitudes ajenas devuelve 404. 60 solicitudes por minuto por consumidor, cuerpo máximo 16 KiB. Estos límites son parámetros técnicos iniciales, sujetos a pruebas.

Las plantillas usan identificadores estables con sufijo .v1 y parámetros documentados. El consumidor no envía texto libre. El recordatorio externo no programa envíos futuros: se solicita al momento elegido por el consumidor. El recordatorio interno de un turno pertenece a Turnos, 24 h antes, con invalidación de versiones obsoletas.

RabbitMQ se utiliza para eventos internos; su topología y credenciales no forman parte del contrato compartido. Si falla el correo de una cancelación por bloqueo, el turno sigue cancelado; se persiste el aviso interno y se reintenta el correo. Los avisos de menores se dirigen al menor y responsable; sin correo propio del menor, el correo va solo al responsable.

## Justificación
HTTP/OpenAPI permite describir operaciones, entrada, resultado y errores sin acceso al broker. La clave por grupo facilita identificación y aislamiento con poca configuración para este TP. HTTPS, configuración externa y ausencia de claves en logs son necesarias para el servicio real.
202 distingue aceptación de resultado. Idempotencia permite repetir solicitudes cuya respuesta se perdió, sin crear deliberadamente otro correo. El consumidor consulta estados sin depender de detalles internos.

## Alternativas consideradas
RabbitMQ para el otro grupo exige acuerdos de broker, colas y operación compartida. JWT requiere un emisor y configuración adicional; podrá evaluarse posteriormente. HTTP para eventos internos obliga a resolver recuperación de mensajes; se conserva RabbitMQ. Texto libre se excluye por decisión funcional del grupo.

## Compatibilidad y publicación
/v1 es la versión mayor; cambios incompatibles en rutas, campos obligatorios, plantillas o significado exigen /v2 y guía de migración. Extensiones compatibles pueden mantener v1. Se conservarán contratos y ADR históricos.
El consumidor usará la URL publicada desde su microservicio responsable. La publicación pública, los responsables de acceso, proveedor de correo y funcionamiento durable están pendientes del siguiente hito; no hay URL productiva acreditada.

## Consecuencias y límites
Se requiere almacenamiento durable antes de 202, control concurrente de claves, reintentos, privacidad y observabilidad. La ventana de 24 h no deduplica indefinidamente. La idempotencia HTTP no garantiza entrega exactamente una vez por un proveedor externo.
El mock usa Go y memoria, valida solicitudes, aísla consumidores y simula resultados. Reiniciarlo pierde sus datos. No envía correos, no publica eventos ni demuestra la persistencia real.

## Referencias
- [OpenAPI 3.1.1](https://spec.openapis.org/oas/v3.1.1.html).
- [HTTP 202](https://www.rfc-editor.org/rfc/rfc9110.html#name-202-accepted).
- [RabbitMQ: confirms y acknowledgements](https://www.rabbitmq.com/docs/confirms).

