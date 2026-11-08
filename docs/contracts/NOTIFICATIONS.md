# API de Notificaciones v1 (ya no es la capacidad publicada)
> **Desde el 23/10/2026 la capacidad publicada del grupo es Turnos** ([README](README.md), [ADR-008](../adr/ADR-008-Contrato-propio.md)). Esta API sigue funcionando con el mismo contrato y se conserva por compatibilidad y como historia; no tiene un consumidor asignado.

Contrato formal: [notifications-v1.openapi.json](notifications-v1.openapi.json), OpenAPI 3.1.1, versión de API **1.1.0** (cambio compatible respecto de 1.0.0, ver [ADR-008](../adr/ADR-008-Contrato-propio.md)).
Estado: servicio real operativo en local (MySQL, envío asíncrono, correos capturados por Mailpit) y mock en memoria para pruebas sin dependencias. URL pública y proveedor de correo real pendientes. El grupo consumidor aún no fue asignado.

## Operaciones
| Método y ruta | Uso |
|---|---|
| GET /v1/templates | Catálogo, parámetros y contenido fijo de las plantillas. |
| POST /v1/notifications | Solicitar un correo; devuelve 202, ID, estado accepted y Location. |
| GET /v1/notifications/{id} | Consultar una solicitud del mismo consumidor. |
| GET /healthz | Estado; no requiere clave. `mode` es `mock` o `service`; el servicio real responde 503 sin almacenamiento. |

Cada consumidor tendrá X-API-Key distinta. La clave queda en su microservicio, no en el frontend. HTTPS en el despliegue real. El consumidor llamará desde el servicio responsable del flujo; la URL pública del proveedor podrá pasar por su gateway. No compartir credenciales de RabbitMQ.

## Plantillas
| templateId | Obligatorios | Opcionales |
|---|---|---|
| operation-confirmed.v1 | operationName, reference | recipientName, scheduledAt |
| operation-cancelled.v1 | operationName, reference | recipientName, reason |
| operation-reminder.v1 | operationName, reference, scheduledAt | recipientName |

Todos los parámetros son strings y se tratan como texto, sin HTML ni sustitución de plantillas anidadas. No se acepta cuerpo, asunto ni texto libre adicional. Límites: operationName/reference 80 caracteres, recipientName 100, reason 200, scheduledAt 35. scheduledAt tiene formato RFC3339 con zona. Cuerpo de solicitud máximo 16 KiB; correo máximo 254 caracteres. Los campos desconocidos o de tipo incorrecto son rechazados. Las tres plantillas son generales y no contienen información clínica.
El recordatorio externo se solicita cuando el consumidor quiere el envío; scheduledAt describe la operación y no programa un envío futuro.

## Idempotencia y estados
Idempotency-Key es obligatoria en POST, de 1 a 128 caracteres alfanuméricos o ._:-. Ventana: 24 h desde la primera aceptación, por consumidor.
Misma clave + datos equivalentes (sin importar orden de propiedades): mismo ID y respuesta original 202. No se extiende la ventana. Cambiar datos con esa clave: 409. Al vencer, puede crearse otro envío. El consumidor debe generar una clave por correo lógico, conservarla ante reintentos y no reutilizarla para otra operación.

accepted significa solicitud aceptada; sent significa aceptada por el proveedor de correo; failed significa fracaso terminal del procesamiento. sent no acredita entrega, lectura ni recepción por la persona. El mock marca simulated=true y simula accepted → sent o failed después de MOCK_DELAY al consultar el estado. Un POST repetido conserva el estado accepted de su respuesta inicial; GET muestra el estado actual.

Límite inicial: 60 solicitudes por minuto por consumidor, contando operaciones autenticadas. 429 incluye Retry-After en segundos. La implementación real responderá 503 si no puede aceptar de forma durable; el mock no reproduce esa falla.

## Errores
| HTTP | code | Acción del consumidor |
|---|---|---|
| 400 | INVALID_JSON / INVALID_IDEMPOTENCY_KEY | Corregir estructura, tipos o clave. |
| 401 | UNAUTHORIZED | Corregir la credencial. |
| 404 | NOT_FOUND | Revisar ID y consumidor; no revela solicitudes ajenas. |
| 409 | IDEMPOTENCY_CONFLICT | Resolver discrepancia; no cambiar la clave para forzar otro envío. |
| 413 | PAYLOAD_TOO_LARGE | Reducir cuerpo. |
| 415 | UNSUPPORTED_MEDIA_TYPE | Usar application/json. |
| 422 | VALIDATION_ERROR | Corregir plantilla, destinatario o parámetros. |
| 429 | RATE_LIMITED | Esperar Retry-After. |
| 503 | SERVICE_UNAVAILABLE | Reintentar con la misma clave y datos. |

Los errores usan {"error":{"code":"…","message":"…"}}. X-Request-ID identifica solicitudes; no usar su texto como credencial.

## Ejecutar y probar
Servicio real: `docker compose up --build` y usar `http://127.0.0.1:8080/v1/...` con una clave de `NOTIF_API_KEYS`. Mock en memoria: requiere Go 1.26 o posterior. Desde la raíz, en PowerShell:
```powershell
$env:MOCK_API_KEYS = 'grupo-demo:solo-demo-local'
go run ./services/notifications/mock
```
En otra terminal:
```powershell
$headers = @{ 'X-API-Key' = 'solo-demo-local'; 'Idempotency-Key' = 'reserva-123' }
$body = Get-Content docs/contracts/examples/confirmation.json -Raw
$result = Invoke-RestMethod http://127.0.0.1:8085/v1/notifications -Method Post -Headers $headers -ContentType application/json -Body $body
$result
Invoke-RestMethod ("http://127.0.0.1:8085/v1/notifications/" + $result.id) -Headers @{ 'X-API-Key' = 'solo-demo-local' }
```
Repetir el POST con los mismos headers/body devuelve el mismo ID. Cambiar reference conservando la clave produce 409.
Para simular fallo, detener el mock, definir $env:MOCK_RESULT = 'failed' y arrancarlo de nuevo. MOCK_DELAY controla la demora (por defecto 1s). La dirección por defecto es 127.0.0.1:8085; MOCK_ADDR permite cambiarla.

Tests: go test -v ./services/notifications/mock.
Verificación HTTP separada: python scripts/check_contract.py (inicia un binario temporal y valida respuestas con el contrato).
La clave de arriba es exclusivamente una muestra local, no una credencial real.

## Historial de versiones
| Versión | Cambio |
|---|---|
| 1.1.0 | `/healthz` admite `mode: service` y `simulated: false`; se documenta 503. Servidor del gateway local. Operaciones de correo sin cambios. |
| 1.0.0 | Versión inicial con mock (Entrega 1). |

## Versionado y limitaciones
/v1 identifica la versión mayor; las plantillas tienen sufijo .v1. Campos obligatorios nuevos, eliminación de operaciones/plantillas o cambios de significado exigen v2 y guía de migración. Las extensiones compatibles mantienen v1 y suben la versión menor; los consumidores deben tolerar parámetros opcionales de respuesta nuevos.
El mock no persiste después de reiniciar, no usa bases ni RabbitMQ, no envía correo, no demuestra disponibilidad pública y mantiene estados en memoria hasta detenerse. No compartirlo como servicio real. El proveedor de correo y su política ante respuesta incierta siguen pendientes.

