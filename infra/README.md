# Dependencias de desarrollo
compose.yaml en la raíz inicia mock, gateway, esqueletos y dependencias previstas.
MySQL usa cuatro bases lógicas. mysql-init.sql no crea tablas ni usuarios de servicio; faltan migraciones y separación efectiva de permisos.
MongoDB: Historia clínica. RabbitMQ: eventos internos. Redis y OpenSearch: proyecciones.
La configuración es local y no acredita integración de servicios con esas dependencias. OpenSearch deshabilita seguridad solo en el entorno local aislado.
Definir credenciales en .env (fuera de Git). docker compose config comprueba la interpolación; docker compose up --build prepara el entorno cuando Docker esté disponible.
Imágenes propuestas para el entorno de la Entrega 1; deberán fijarse por digest al validar el despliegue reproducible. No se ejecutó Docker Compose en esta sesión.

