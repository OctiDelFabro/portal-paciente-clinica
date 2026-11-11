# Correcciones de la revisión de main

Fecha: 10/11/2026. Rama: `fix/main-revision-completa`, creada desde `main` en `d1c46b0b8375f8af4c49fb838f5be0c0e6159feb`. Se conservan `compose.hostinger.yaml` y el aislamiento del script de inicialización de MySQL incorporados en main. No se modifica el despliegue.

## Defectos y cambios

| Hallazgo | Cambio implementado | Verificación |
|---|---|---|
| F01 Identificador de profesional | Adaptación de `id` a `doctorId` tanto en búsqueda como detalle. | Test del cliente real; recorrido de navegador preparado para CI. |
| F02 Lista vacía | Cliente consume `appointments`; una respuesta desconocida genera error explícito. | Test de envelope del cliente; pantallas paciente/médico en prueba de navegador. |
| F03 Habilitación ignorada | Turnos consulta Pacientes mediante contrato interno autenticado. Se elimina la habilitación implícita del paciente desconocido. Clínica comprueba habilitación actual. | Tests de alta pendiente, desconocido, dependencia caída y política clínica. |
| F04 Contactos semilla | Notificaciones consulta contactos actuales de Pacientes y del responsable adulto válido. Una caída es reintentable, distinta de no tener correo. | Test de contactos de cuenta nueva, menor/responsable, sin correo y caída. |
| F05 Reserva fuera de agenda | Dominio comprueba día, apertura/cierre y duración en ART; portal/socios comparten regla. Se permiten inicios intermedios dentro de la ventana, siempre sin superposición. | Tests de dominio y errores HTTP; fixtures de integración/carga corregidos. |
| F06 Reintento no reindexa | Aun ante un evento ya aplicado a SQL se vuelve a indexar desde el estado actual. | Test con fallo inicial del índice y reentrega posterior. |
| F07 Agotamiento de pool | Crear y bloquear recursos usa la misma conexión transaccional y un orden estable. | Driver de prueba con pool de una conexión; concurrencia SQL real preparada en integración. |
| F08 Rol administrativo inaccesible | JWT/gateway/frontend admiten `admin` y `reception`. Registro público sigue limitado a pacientes. Cuentas demo de personal optativas y pantalla operativa. | Tests de roles admitidos/rechazados y verificación reservada a personal. |
| F09 Especialidad en texto | La consulta combina nombre y especialidades normalizadas; conserva filtros y orden. | Test de consulta; caso “pediatría” agregado a integración OpenSearch. |
| F10 Suscripciones ausentes | Relays crean/verifican toda la topología antes de publicar; volumen persistente RabbitMQ. | Test unitario de enlaces; test de broker vacío con publicación antes de consumidores. |
| F11 Documentación contradictoria | README, frontend, arquitectura, SPEC, verificación, ADR-009 e historia actualizados. Parámetros y mediciones históricas separados. | Revisión de referencias y comandos; no se presentan benchmarks anteriores como nueva evidencia. |
| F12 Redirección con clave | Cliente energético rechaza redirecciones; los nuevos clientes internos también. | Test con redirección a otro host y credencial sintética; destino no recibe la clave. |
| F13 Motivo incompatible | Hasta 200 caracteres Unicode sin saltos/NUL; SQL ampliado sin borrar datos. | Dominio/HTTP y persistencia real preparada en integración. |
| F14 Memoria sin límite | Máximo de 10.000 claves; rechazo de nuevas si siguen ocupadas, limpieza como máximo una vez por segundo. | Test con todos los buckets activos y recuperación posterior. |
| F15 Timeout incompleto | Temporizador activo hasta consumir el JSON, con errores normalizados y protección contra 401 de sesión anterior. | Tests de stream detenido y respuesta de sesión previa. |
| F16 Precio variable | Snapshot de base y tarifa por kWh desde la reserva; reestimación usa la tarifa capturada. | Tests de cambio de configuración con energía estimada/pendiente; persistencia preparada en integración. |
| F17 Agenda truncada | Páginas de 100, próximos activos primero, `nextPage` y botón para cargar más en ambas pantallas. | Test MySQL preparado con 105 cancelados y un próximo activo, dos páginas y ambos roles. |

## Ajustes complementarios

Recepción y administración pueden verificar pacientes y registrar vínculos adulto–menor comprobando edad y habilitación. Historia Clínica tiene lectura por paciente objetivo, autorizada para médico con relación no cancelada o responsable adulto verificado de menor; un fallo de las dependencias no permite el acceso. La agenda del médico permite abrir la historia y registrar notas. Se añadieron controles de permisos y secretos de servicio separados de JWT.

## Validación y límites de la revisión inicial (F01–F17)

- `go test -race ./...`: pasó después de corregir una carrera del fixture HTTP del proveedor.
- `go vet ./...`: pasó.
- `npm test`: pasaron los cuatro tests del cliente TypeScript.
- `npm run build`: pasó TypeScript y Vite.
- `go test -tags integration -run '^$' ./...`: compiló; no ejecutó pruebas de integración.
- Docker no está disponible en el entorno de edición. La prueba local de Chromium fue bloqueada por permisos del entorno al crear su socket. Se agregó el workflow para ejecutar infraestructura real y los cinco recorridos de navegador. GitHub no inició ninguno de sus jobs: informó que la cuenta está bloqueada por un problema de facturación (ejecución 42893377415). No hubo resultados remotos de pruebas. Los recorridos de navegador usan respuestas HTTP simuladas con forma del servicio, no infraestructura clínica real.

## Migración y operación

Antes de actualizar datos a conservar, hacer backup. Inicio agrega columnas de precio y amplía `cancel_reason` a `VARCHAR(200)`. Las reservas antiguas nunca guardaron su base histórica: se fija una vez la tarifa configurada al actualizar. Una configuración posterior no cambia ese snapshot. La clave `INTERNAL_API_KEY` es nueva, compartida por los servicios y de al menos 32 bytes; sin ella el inicio falla explícitamente. No se envía a React ni por rutas del gateway.

El volumen nombrado de RabbitMQ conserva la topología y mensajes tras recrear contenedores futuros. No migra por sí solo los mensajes de un broker anterior sin volumen: drenar o respaldar antes de recrearlo. No se debe usar `docker compose down -v` sobre datos que se quieran conservar.

## Ampliación tras la auditoría funcional del 11/11/2026

Los pendientes de reprogramación atómica, edición/bloqueos de agenda, gestión de menores, alta/edición/asignación por recepción y recordatorios/bandeja tienen implementación y pruebas en la nueva rama. También se cierran JSON sobrante en Médicos, unicidad de DNI, restricción de atención a menores, notas futuras, cotización fuera de horario, horizonte/orden de búsqueda, mensaje de habilitación y métricas por gateway.

Ver [trazabilidad, reglas, verificación y despliegue](AUDITORIA-FUNCIONAL-CORRECCIONES.md). La llamada coordinada al proveedor real y la validación operativa del VPS permanecen a cargo del equipo; no se acreditan mediante tests simulados.
