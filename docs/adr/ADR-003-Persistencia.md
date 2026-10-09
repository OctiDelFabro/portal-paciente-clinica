# ADR-003 — Persistencia inicial
**Decisión D3 · Estado: diseño inicial para Entrega 1.** Las elecciones proceden del diagrama original; sus garantías se deberán demostrar al implementar.

## Contexto
El portal administra reservas con restricciones temporales, perfiles, vínculos entre pacientes y responsables, registros clínicos y notificaciones. Necesita al menos almacenamiento relacional y no relacional. No se dispone todavía de volúmenes medidos ni de resultados de carga.

## Decisión
| Servicio | Almacenamiento propio | Acceso previsto y motivo |
|---|---|---|
| Turnos | MySQL / InnoDB | Consultar por médico, paciente y rango temporal; registrar reserva, bloqueo o reprogramación en una transacción. |
| Pacientes | MySQL / InnoDB | Consultar habilitación, contactos y vínculos verificados; asegurar integridad de relaciones locales. |
| Médicos | MySQL / InnoDB | Perfiles y relación de muchos a muchos con especialidades. OpenSearch será una proyección de búsqueda. |
| Notificaciones | MySQL / InnoDB | Solicitudes, estados por canal, intentos, plantillas, claves de idempotencia e inbox para eventos. |
| Historia clínica | MongoDB | Documentos de atención con estructura que puede variar; consulta paginada por paciente y fecha. |

En desarrollo local, un servidor MySQL puede alojar cuatro bases con credenciales separadas. Ningún servicio tendrá permisos sobre la base de otro. Los identificadores de otros servicios son referencias opacas; no hay claves foráneas entre servicios.

Redis y OpenSearch son datos derivados, recuperables. Redis no confirma reservas; OpenSearch no es autoridad de agenda ni perfiles. La indisponibilidad de una proyección no permite aceptar una reserva sin validar su disponibilidad real.

### Consistencia de Turnos
Propuesta inicial para D4: serializar en MySQL las escrituras de agenda y reservas mediante filas de recurso por médico y paciente, con bloqueo transaccional en orden estable. Bajo esos bloqueos se comprobará la intersección de intervalos activos; una restricción única sobre la hora de inicio no basta para impedir solapamientos de distinta duración. Todos los caminos de escritura deberán usar el mismo protocolo. La reprogramación será una transacción: si el destino no puede confirmarse, el original continúa vigente y se informa al paciente. Los bloqueos de agenda y cancelaciones afectadas se registrarán juntos. No se afirma que este mecanismo esté implementado.

### Historia clínica
Se propone una colección de atenciones separadas, referenciadas por patientId, con schemaVersion y validación de campos obligatorios; índice por patientId y fecha. Evita un único documento que crezca sin límite. MongoDB no sustituye la autorización: los permisos se verifican antes de consultar y no se devuelve información clínica si una validación necesaria no puede obtenerse. Los campos y correcciones de atenciones siguen pendientes de acuerdo.

### Notificaciones
La implementación real registrará la solicitud antes de responder 202. Una restricción única por consumidor y clave, junto con su vencimiento, resolverá la idempotencia concurrente. Se conservarán estados separados para correo y aviso del portal. Una outbox transaccional vinculará cambios de negocio con eventos pendientes de publicación.

El mock entregado utiliza memoria, pierde solicitudes al reiniciar y simula resultados: no demuestra MySQL, MongoDB, RabbitMQ, outbox ni envío real.

## Alternativas consideradas
- Todo en MySQL: simplifica operación, pero no cubre los dos tipos de almacenamiento exigidos y exige modelar de forma relacional la variación de atenciones.
- Todo en MongoDB: es posible diseñar consistencia, pero añade complejidad para restricciones y relaciones locales que encajan en tablas.
- Base común entre servicios: reduce configuración y acopla esquemas y permisos. Se descarta el acceso cruzado.

## Consecuencias y limitaciones
Se necesitan migraciones MySQL y validación/versionado de documentos MongoDB. No existe una transacción distribuida automática entre servicios. Outbox, consumidores idempotentes y permisos se implementarán antes de afirmar garantías. Faltan política de retención, copias de respaldo y cifras de capacidad.

## Referencias
- [MySQL: lecturas con bloqueo](https://dev.mysql.com/doc/refman/8.4/en/innodb-locking-reads.html).
- [MongoDB: modelado según patrones de acceso](https://www.mongodb.com/docs/manual/data-modeling/).
- Alcance: ../SPEC.md. Límites: ADR-001-Limites-de-servicios.md.

