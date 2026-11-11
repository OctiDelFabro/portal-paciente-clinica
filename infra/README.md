# Dependencias de desarrollo
compose.yaml en la raíz inicia el gateway, los seis servicios (Turnos con dos réplicas), el frontend, el stub del proveedor energético, el mock de Notificaciones y sus dependencias.
MySQL usa cuatro bases lógicas (`mysql-init.sql`) y un usuario por servicio con permisos solo sobre su base (`mysql-users.sh`, ADR-003). Cada servicio aplica su propio esquema al iniciar.
MongoDB: Historia clínica. RabbitMQ: eventos internos. Redis: caché de disponibilidad (ADR-007). OpenSearch: búsqueda de médicos (ADR-006).
Observabilidad: Prometheus (`prometheus.yml`), Grafana (`grafana/`) y Jaeger. Pruebas de carga k6 en `k6/`.
OpenSearch deshabilita seguridad solo en el entorno local aislado.
Definir credenciales en .env (fuera de Git). `docker compose config` comprueba la interpolación y `docker compose up --build` levanta el entorno.
`mysql-users.sh` solo corre al inicializar un volumen vacío: con un volumen existente, crear los usuarios a mano o recrearlo con `docker compose down -v`.
