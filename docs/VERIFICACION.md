# Verificación del paquete
## Entrega 2
Ejecutado en la sesión de desarrollo (Go 1.26.1, Windows):
- `go vet ./...` y `go vet -tags integration ./...`: sin observaciones.
- `go test ./...`: correcto. Cubre las reglas de dominio de Turnos (24 h, solapamiento por médico y paciente, turnos adyacentes y cancelados, cancelación), casos de uso con 20 reservas concurrentes, mapeo HTTP de errores, validación y plantillas de Notificaciones, backoff, circuit breaker, clasificación de mensajes mal formados, regla de menores, rechazos de la API pública, rate limit, timeouts y reintentos del gateway, y la propagación de correlación.
- `docker compose config --quiet` con valores de prueba: válido.

**No ejecutado (Docker no disponible en esa sesión):** `docker compose up`, `go test -tags integration ./...` (MySQL y RabbitMQ reales), recorrido manual del README y trazas en Jaeger. Ejecutarlos antes de presentar.

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

