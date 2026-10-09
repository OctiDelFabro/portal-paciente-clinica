# ADR-005 — Comunicación inicial
**Decisión D5 · Estado: diseño inicial para Entrega 1.** HTTP para consultas y comandos que necesitan respuesta; RabbitMQ para eventos internos. Los parámetros siguientes son valores técnicos iniciales para validar durante implementación.

## Contexto y decisión
React/TypeScript accede por el gateway a los servicios. Los servicios consultan capacidades ajenas mediante HTTP; no leen bases de otros servicios. El grupo consumidor solicita correos por HTTP a la capacidad publicada de Notificaciones, con clave de API propia. El acceso público se publicará a través de la ruta del gateway del proveedor; el consumidor debe iniciar la llamada desde su microservicio responsable, conforme a la consigna.

| Interacción | Mecanismo | Resultado esperado |
|---|---|---|
| Frontend → gateway → servicio | HTTP/JSON | Resultado del comando o consulta; errores expresos. |
| Turnos → Pacientes / Médicos | HTTP/JSON | Habilitación, vínculo y perfil necesarios para operar. |
| Historia clínica → Pacientes / Turnos | HTTP/JSON | Relaciones que habilitan acceso. Si falla la validación, se rechaza la consulta temporalmente sin exponer registros. |
| Consumidor externo → API de Notificaciones | HTTP/JSON + X-API-Key | 202 e identificador; GET posterior para el resultado. |
| Turnos → RabbitMQ → Notificaciones | Evento de dominio | Avisos internos y correo independientes del resultado del turno. |

## Tiempos de espera y reintentos
- Presupuesto inicial del gateway: 5 s por solicitud. Cada llamada interna tiene hasta 2 s, dentro del presupuesto total; conexión hasta 500 ms. No se ejecutan reintentos fuera del plazo original.
- Consultas GET: hasta un reintento ante error de red, 502, 503 o 504, con espera de 100–300 ms y presupuesto restante suficiente.
- Escrituras: no reintentar automáticamente sin idempotencia. Una respuesta ausente no demuestra que el comando no se ejecutó.
- API externa de Notificaciones: cliente con timeout total de 5 s. Tras pérdida de respuesta o 503, repetir el POST con la misma Idempotency-Key y los mismos datos. Hasta dos reintentos adicionales con esperas de 1 s y 2 s; ante 429 respetar Retry-After y detener el reintento inmediato.
- Errores 400, 401, 409, 413, 415 y 422 requieren corrección; no se reintentan sin cambiar la causa. No cambiar la clave para eludir un 409.
- Respuestas tardías o peticiones canceladas dejan de usarse para autorizar otra operación. Si no se conoce el resultado de una escritura, el usuario ve estado indeterminado y se consulta/reconcilia; no se afirma un fracaso definitivo.

Estos valores no fueron medidos. D10 revisará las políticas tras pruebas con dependencias reales.

## Eventos iniciales
Exchange topic durable: portal.events.v1.
Rutas iniciales: turnos.reservado.v1, turnos.cancelado.v1, turnos.reprogramado.v1 y turnos.recordatorio-solicitado.v1.
Contrato procesable: ../contracts/turnos-events-v1.schema.json.

El sobre incluye eventId, eventType, version, occurredAt, correlationId y data. data contiene turnId, patientId, doctorId, startAt, endAt y appointmentVersion. Reprogramación agrega previousStartAt/previousEndAt; cancelación agrega reasonCode. No se incluyen historia clínica ni credenciales. Los contactos se resuelven por el contrato de Pacientes.

Turnos registra operación y outbox en una misma transacción. Un publicador reintenta las entradas pendientes, usa mensajes persistentes, publicación mandatory y publisher confirms; una confirmación del broker no demuestra envío del correo. Notificaciones reconoce el mensaje solo después de persistir su inbox y los trabajos derivados. Se asume entrega al menos una vez; un índice único por eventId y restricciones por evento/canal/destinatario evitan trabajos duplicados.

## Mensajes fallidos y canales independientes
Se proponen tres reintentos internos, tras 5 s, 30 s y 120 s. Después, una cola de mensajes fallidos conserva el evento y motivo para inspección y reejecución controlada con el mismo eventId. No se usa un bucle infinito de reencolado inmediato. Las colas y las políticas de dead-letter/reintento se configurarán y probarán con RabbitMQ real.

El aviso del portal y el correo tienen estados independientes. Una falla del correo no revierte una reserva ni una cancelación, y no impide persistir el aviso interno. Si falla Notificaciones, el evento queda pendiente y el aviso puede aparecer más tarde; no se promete publicación inmediata durante una caída.

## Recordatorios
Turnos es dueño del horario vigente. Un programador buscará reservas activas que alcancen startAt − 24 h, usando una clave única por turno y versión. Cancelar invalida el trabajo y reprogramar crea uno para la nueva versión. El consumidor verifica la versión/estado vigente antes de enviar; un aviso ya entregado al proveedor no puede retirarse. En el límite exacto de 24 h, se omite un recordatorio redundante generado junto con la confirmación (propuesta técnica pendiente de validar con el grupo).

## Alternativas y consecuencias
HTTP para todo reduce transportes, pero obliga a construir recuperación de eventos pendientes. RabbitMQ para el otro grupo exige compartir operación y credenciales del broker. Se adopta HTTP externo y mensajería interna.
Se acepta consistencia eventual en avisos, posible repetición de entregas de eventos y complejidad de outbox/inbox. La idempotencia de trabajos no garantiza por sí sola un único correo si el proveedor acepta un envío y se pierde su respuesta; se evaluará el soporte de idempotencia del proveedor.

## Estado verificable
El mock implementa únicamente el contrato HTTP, con memoria e idempotencia durante su ejecución. No implementa eventos reales, timeouts interservicio, reintentos de correo ni DLQ. El grupo proveedor externo, su contrato y el servicio consumidor siguen pendientes de asignación.

## Referencias
- [RabbitMQ: publisher confirms y consumer acknowledgements](https://www.rabbitmq.com/docs/confirms).
- [RabbitMQ: dead-letter exchanges](https://www.rabbitmq.com/docs/dlx).
- [RFC 9110: semántica HTTP](https://www.rfc-editor.org/rfc/rfc9110.html#name-202-accepted).

