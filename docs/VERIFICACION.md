# Verificación del paquete
## Entrega 2
Ejecutado en la sesión de desarrollo (Go 1.26.1, Windows):
- `go vet ./...` y `go vet -tags integration ./...`: sin observaciones.
- `go test ./...`: correcto. Cubre las reglas de dominio de Turnos (24 h, solapamiento por médico y paciente, turnos adyacentes y cancelados, cancelación), casos de uso con 20 reservas concurrentes, mapeo HTTP de errores, validación y plantillas de Notificaciones, backoff, circuit breaker, clasificación de mensajes mal formados, regla de menores, rechazos de la API pública, rate limit, timeouts y reintentos del gateway, y la propagación de correlación.
- `docker compose config --quiet` con valores de prueba: válido.

Ejecutado con Docker 29.9.1 (2026-10-20):
- `go test -tags integration ./services/...`: correcto contra MySQL 8.4 y RabbitMQ 4.1 reales. 15 reservas concurrentes solapadas dejan una sola; 30 POST con la misma clave crean una sola notificación; un evento duplicado no crea trabajos y uno mal formado llega a la DLQ.
  - Este test encontró un **deadlock 1213** en la idempotencia (gap locks de REPEATABLE READ). Se corrigió con READ COMMITTED y reintento.
- `docker compose up --build` con un `.env` de prueba: todos los contenedores en ejecución y los `/healthz` en `ok`.
- Recorrido por el gateway: 201 al reservar; 409 `SLOT_UNAVAILABLE`, 422 `LEAD_TIME_TOO_SHORT` y 403 `PATIENT_NOT_ENABLED` en los rechazos. El correo llega a Mailpit.
- Jaeger: una traza de 6 spans: `gateway POST /api/turnos/` → `gateway HTTP POST` → `turnos POST /appointments` → `turnos publish turnos.reservado.v1` → `notifications consume turnos.reservado.v1` → `notifications send email`. Los logs de los tres servicios comparten `correlation_id` y `trace_id`.
- Caída de RabbitMQ: la reserva responde 201, `outbox_pending` llega a 1 con `outbox_oldest_age_seconds` de unos 6 s, y al levantar el broker la outbox se vacía y llega el correo (menor `pat-003` → responsable).
- Prometheus con los tres objetivos `up`; el tablero de Grafana se provisiona.
- Se encontró y corrigió un `X-Correlation-ID` duplicado en las respuestas del gateway.

`python scripts/check_contract.py` contra el mock: falla de forma intermitente en Windows en "limite de cuerpo" (ConnectionResetError). Se comprobó que ocurre igual en `main` (1 de 4 ejecuciones correctas), así que no lo causó esta entrega.

## Entrega 1
## Ejecutado
- go test -v ./services/notifications/mock: seis pruebas correctas.
- Incluye 30 POST concurrentes con la misma clave; una única solicitud y un mismo ID.
- Comprueba idempotencia semántica, conflicto, autenticación, aislamiento de consumidores, vencimiento de 24 h, tres plantillas, límite de solicitudes y fallos simulados.
- go test ./...: todo el backend Go compila; los esqueletos no tienen pruebas de dominio porque aún no implementan esas funciones.
- python scripts/check_contract.py: 18 comprobaciones HTTP correctas, con un servidor real en loopback. Resultado detallado en verification-http.json.
- El comprobador HTTP evalúa los campos y restricciones usados del contrato; no equivale a un validador completo de OpenAPI.
- docker compose --env-file .env.example config --quiet: configuración inicial validada por la CLI, sin iniciar contenedores.

## No ejecutado ni acreditado
Docker Engine no estaba disponible. No se iniciaron imágenes ni dependencias.
No se instalaron ni compilaron dependencias React/TypeScript en esta sesión.
No hubo correo real, bases de datos, RabbitMQ, despliegue público, integración con otro grupo ni pruebas del dominio clínico.

## Reproducir
Desde la raíz: go test ./... y python scripts/check_contract.py.
Docker operativo: copiar .env.example a .env, reemplazar valores y ejecutar docker compose up --build.
El mock requiere MOCK_API_KEYS. No usar sus muestras como credenciales reales.

