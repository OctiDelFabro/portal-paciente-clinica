# Verificación del paquete
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

