# Postmortem — Lecciones aprendidas y análisis de incidentes

Este documento registra las lecciones aprendidas, fallas operativas identificadas durante el ciclo de desarrollo y pruebas de carga, las causas raíz subyacentes y las soluciones de ingeniería adoptadas para la entrega final del sistema.

---

## Incidente 1: Omisión de inicialización en volúmenes persistentes de MySQL

### Contexto y Síntomas
Durante el despliegue de la Entrega Final, al incorporar los nuevos microservicios `pacientes` y `medicos`, los servicios no lograban iniciar, arrojando errores reiterados:
`Error 1045 (28000): Access denied for user 'pacientes'@'172.19.0.x' (using password: YES)`

### Causa Raíz
El contenedor oficial de MySQL (`mysql:8.4`) solo ejecuta los scripts ubicados en `/docker-entrypoint-initdb.d/` cuando el directorio de datos `/var/lib/mysql` está completamente vacío. Dado que el volumen `mysql-data` persistía el estado de la Entrega 2, el motor inició directamente omitiendo los nuevos scripts de creación de usuarios y bases de datos (`01-databases.sql` y `02-users.sh`).

### Solución y Aprendizaje
1. Se diseñaron scripts de aprovisionamiento de bases y usuarios estrictamente idempotentes (`CREATE DATABASE IF NOT EXISTS`, `CREATE USER IF NOT EXISTS`, `GRANT ALL ON ...`).
2. Se implementó un comando de reconciliación post-arranque que reaplica los permisos dentro del contenedor sin requerir borrar los volúmenes de datos existentes.
3. **Lección**: Nunca depender de scripts de entrada de Docker para migraciones continuas en bases de datos persistentes. La evolución del esquema debe ser gestionada por los propios microservicios al conectar (`CREATE TABLE IF NOT EXISTS` y verificación en `information_schema.COLUMNS`).

---

## Incidente 2: Vulnerabilidad de suplantación de identidad por Hop-by-Hop Headers en Go ReverseProxy

### Contexto y Síntomas
Durante la verificación independiente de seguridad en la Entrega Final (L8), se descubrió que un atacante podía enviar peticiones al Gateway con el encabezado:
`Connection: X-Patient-Id` acompañado de `X-Patient-Id: victima_123`.
Como resultado, el servicio de backend recibía una solicitud sin identidad o con una identidad alterada, eludiendo los controles del Gateway.

### Causa Raíz
De acuerdo con la especificación RFC 7230, los encabezados listados dentro de la cabecera `Connection` son considerados de salto a salto (*hop-by-hop*) y deben ser descartados antes de retransmitir el paquete HTTP. El componente estándar `net/http/httputil.ReverseProxy` de Go eliminaba cualquier encabezado listado en `Connection` después de que el middleware de autenticación del Gateway hubiera verificado el JWT y asignado `X-Patient-Id`.

### Solución y Aprendizaje
1. En `gateway/cmd/proxy.go`, se implementó una sanitización previa de la cabecera `Connection`, eliminando cualquier referencia a encabezados internos de identidad (`X-Patient-Id`, `X-User-Role`) antes de invocar a `ReverseProxy`.
2. Se redactaron pruebas de regresión específicas (test B1) que validaron la falla en rojo y el pase en verde.
3. **Lección**: Los componentes de proxy inverso estándar aplican transformaciones semánticas de transporte que pueden interactuar de forma no intuitiva con la propagación de identidades de aplicación. Las cabeceras críticas de seguridad deben ser saneadas y blindadas en ambos extremos.

---

## Incidente 3: Interbloqueos (Deadlocks 1213) por Gap Locking en MySQL

### Contexto y Síntomas
Durante las pruebas de concurrencia de la API de Notificaciones (30 solicitudes simultáneas con la misma clave de idempotencia), se produjeron excepciones intermitentes:
`Error 1213 (40001): Deadlock found when trying to get lock; try restarting transaction`.

### Causa Raíz
Bajo el nivel de aislamiento predeterminado de MySQL InnoDB (`REPEATABLE READ`), las verificaciones de unicidad sobre filas inexistentes colocan bloqueos de intervalo (*gap locks* y *next-key locks*). Múltiples hilos concurrentes intentando insertar sobre el mismo intervalo generaban dependencias circulares de bloqueo, forzando a MySQL a abortar una de las transacciones.

### Solución y Aprendizaje
1. Se configuró explícitamente el nivel de aislamiento en `READ COMMITTED` para los almacenes transaccionales de Notificaciones y Turnos. En este nivel, InnoDB no utiliza *gap locks* para sentencias de lectura o comprobación de unicidad.
2. Para el flujo crítico de Turnos, se reemplazó la dependencia de bloqueos implícitos por un esquema determinista de bloqueo pesimista (`booking_locks` con `SELECT ... FOR UPDATE` ordenado alfabéticamente).
3. Se añadió un middleware de reintento automático con backoff ante errores 1213 transitorios.
4. **Lección**: Para sistemas de alto volumen con inserciones concurrentes e idempotencia, `REPEATABLE READ` introduce contención nociva. `READ COMMITTED` junto con bloqueos pesimistas ordenados en la capa de aplicación ofrece un throughput significativamente superior y predecible.

---

## Incidente 4: Corrupción de scripts por saltos de línea CRLF en entornos Windows/Linux

### Contexto y Síntomas
Al iniciar los contenedores de Docker en Windows, el script `mysql-users.sh` abortaba abruptamente con el error:
`/docker-entrypoint-initdb.d/02-users.sh: line 6: set: -: invalid option`.

### Causa Raíz
Git en Windows, con la configuración por defecto `core.autocrlf=true`, convertía los saltos de línea de los archivos a formato DOS (`\r\n`). Al montarse el script dentro del contenedor Linux Alpine, el intérprete `/bin/sh` interpretaba el retorno de carro `\r` como parte del argumento del comando (`set -eu\r`), el cual no es un flag válido.

### Solución y Aprendizaje
1. Se convirtieron los scripts a saltos de línea UNIX (`\n`) y se configuró `.gitattributes` en la raíz del repositorio para garantizar que todos los archivos `.sh`, `.sql` y archivos de configuración mantengan saltos LF independientemente del sistema operativo del desarrollador.
2. **Lección**: En proyectos multiplataforma con contenedores Docker montando volúmenes desde el host, los finales de línea deben fijarse rígidamente en el control de versiones.

---

## Incidente 5: Fallas de autorización por arquitectura "Fail-Open"

### Contexto y Síntomas
En las primeras iteraciones de Turnos, las solicitudes `POST /appointments` y `GET /appointments/{id}` permitían ejecutarse si la cabecera `X-Patient-Id` estaba ausente, cayendo en un fallback que confiaba en el `patientId` recibido en el cuerpo JSON de la petición.

### Causa Raíz
Deuda técnica arrastrada desde la Entrega 2, cuando el microservicio funcionaba de manera aislada antes de que el Gateway centralizara la autenticación JWT.

### Solución y Aprendizaje
1. En la tarea T12, se refactorizaron todos los endpoints del portal bajo el principio de seguridad **Fail-Closed**: cualquier solicitud a las rutas del portal que carezca de `X-Patient-Id` es rechazada de inmediato con `401 Unauthorized`.
2. Se introdujo el rol profesional (`doctor`) y la verificación estricta de asignación de encuentros en Historia Clínica (`403 Forbidden` ante médicos no asignados al turno).
3. **Lección**: Nunca permitir que un microservicio mantenga modos permisivos de desarrollo o retrocompatibilidad con clientes anónimos en rutas que manejan datos sensibles o transacciones de negocio.
