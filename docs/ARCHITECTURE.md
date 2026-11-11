# Arquitectura del portal

Estado del código después de la revisión de `main` del 10/11/2026. Los requisitos objetivo están en [SPEC](SPEC.md); las funciones pendientes y la matriz F01–F17 están en [CORRECCIONES-REVISION](CORRECCIONES-REVISION.md).

## Componentes y propiedad de datos

| Componente | Responsabilidad | Persistencia |
|---|---|---|
| React/TypeScript | Interfaz por rol, adaptación de DTO y estado de solicitudes | Sesión en navegador |
| Gateway Go | JWT, limpieza de identidad, límites y balanceo Turnos | Estado de salud y token buckets en memoria |
| Turnos, hexagonal | Disponibilidad, agenda, reservas, precios, API de socios | MySQL 8.4; Redis 7.4 como caché |
| Pacientes, capas | Registro/login, perfiles, habilitación y responsables | MySQL 8.4 |
| Médicos, hexagonal | Perfiles, especialidades, búsqueda y proyección de reservas | MySQL 8.4; OpenSearch 3.0.0 |
| Historia Clínica, capas | Encuentros, notas y autorización clínica | MongoDB 8.0, base `historia_clinica` |
| Notificaciones, capas | API de correo, eventos, avisos, reintentos | MySQL 8.4; SMTP a Mailpit local |
| RabbitMQ 4.1 | Eventos `portal.events.v1`, reintentos y DLQ | Colas durables y volumen nombrado |

No se accede a bases de otros servicios. Pacientes expone `GET /internal/patients/{id}` con `X-Service-Key` y Turnos `GET /internal/relationships?doctorId=...&patientId=...`. Se requiere `INTERNAL_API_KEY` de 32 bytes o más al iniciar. El gateway no expone estas rutas. Los clientes internos tienen timeout de 400 ms, rechazan redirecciones y no reciben contraseñas ni hashes.

## Entradas y autorización

El gateway tiene dos escuchas: `8080` para el portal (`/api/*`, JWT) y métricas; `8088` para socios (`/v1/*`, API key) y `/healthz`. El despliegue Hostinger expone únicamente la segunda por HTTPS. El frontend se ejecuta localmente; no contiene claves de servicio.

Pacientes emite JWT HS256 con `sub`, `role`, `iss`, `iat` y `exp`; gateway valida firma, issuer, expiración, identidad y rol (`patient`, `doctor`, `reception`, `admin`), elimina los headers de identidad del cliente e inyecta los verificados. El registro público no acepta roles. Las cuentas demo de personal son optativas mediante `DEMO_PATIENT_PASSWORD`.

Turnos consulta a Pacientes antes de reservar; un JWT no sustituye la habilitación. Un paciente desconocido se rechaza y una dependencia caída produce 503, sin inventar habilitación. Historia Clínica consulta habilitación actual y relación: propio paciente, responsable adulto verificado de menor o médico con turno no cancelado. Recepción y administración no leen contenido clínico. Las notas requieren además asignación del profesional y que el encuentro ya haya comenzado.

## Reserva, concurrencia y lectura

La transacción adquiere las filas de `booking_locks` ordenadas por recurso, usando su misma conexión SQL para crearlas y bloquearlas. Se comprueban los solapamientos semiabiertos `[inicio, fin)`, anticipación de 24 h, días/horas y duración. No se promete ausencia de deadlocks de InnoDB: los errores 1213/1205 reintentan la transacción hasta tres veces.

La agenda inicial de Turnos mantiene dos profesionales semilla, de lunes a viernes: `doc-001`, 09:00–13:00, 30 min; `doc-002`, 14:00–18:00, 20 min. Perfiles activos y especialidades se consultan en Médicos. Se admiten inicios intermedios dentro de la ventana de atención; la grilla mostrada es una sugerencia. Portal y socios aplican las mismas reglas de escritura. La agenda semanal/duración se persiste en `doctor_agendas`; bloqueos en `agenda_blocks`. Ediciones, reservas, reprogramaciones y bloqueos serializan por médico y pacientes afectados; cancelaciones y eventos se confirman juntos.

Disponibilidad usa Redis con TTL de **30 s** e invalidación después del commit. Una falla de Redis vuelve al cálculo autoritativo; una lectura cacheada nunca autoriza una reserva. Las listas de turnos sirven páginas de 100, próximos activos primero, luego anteriores activos y cancelados, con desempate por ID y `nextPage=0` al terminar. La paginación por offset puede cambiar entre solicitudes si se modifica la agenda; la interfaz deduplica por ID y refresca las páginas cargadas.

## Eventos y búsqueda

Turno y evento outbox se escriben en la misma transacción. Antes de publicar, ambos relays inicializan la topología completa: colas de Notificaciones, Historia y Médicos, sus enlaces, DLQ y colas de reintento de 5/30/120 s. Una confirmación `mandatory` no prueba por sí sola que estén presentes todas las suscripciones; por eso las declara el productor. Se conserva la entrega al menos una vez y cada consumidor debe ser idempotente.

Médicos actualiza MySQL y luego OpenSearch. Las reentregas reindexan desde el estado actual aun si SQL ya aplicó el evento, para reparar un fallo entre ambas escrituras. Búsqueda combina nombre y códigos de especialidad normalizados (tildes y espacios), conserva filtros y paginación. No existe búsqueda SQL de contingencia ante caída de OpenSearch.

## Proveedor energético y precio

Turnos consume `ArquitecturasoftwareRenewableEnergy` con contrato versionado en `docs/contracts/providers/renewable-energy-v1`. El presupuesto inline es 1.200 ms y el de reestimación 5 s; el circuito abre tras cinco fallos durante 30 s. Se rechazan las redirecciones para evitar reenviar `X-API-Key` a otro origen. La reserva se confirma aunque la energía quede `pending`; las llamadas se realizan después del commit, fuera de los bloqueos.

Cada reserva nueva conserva precio base y tarifa por kWh. La energía pendiente se calcula con esa tarifa original. Las filas antiguas se completan una vez con la configuración del primer inicio actualizado, porque el precio histórico no era recuperable. Los socios mantienen su precio separado en `partner_bookings`.

## Límites operativos

Los token buckets son locales a un gateway: máximo 10.000 claves, rechazo temporal de nuevas claves si no hay espacio y limpieza como máximo una vez por segundo. Varias réplicas multiplicarían el límite de tráfico. JWT no tiene revocación inmediata general; los controles de reserva y clínica sí consultan habilitación actual.

El balanceador sondea cada 2 s con 500 ms de timeout y retira una réplica tras dos fallos. Una escritura puede fallar antes de que se detecte la caída; no se reenvía automáticamente por su resultado incierto. El cliente informa esa incertidumbre y su timeout cubre headers y cuerpo. Las métricas antiguas de carga no acreditan el rendimiento de esta rama; ver [VERIFICACION](VERIFICACION.md).

Los flujos de recepción, responsables, avisos y recordatorios se detallan en [la corrección funcional](AUDITORIA-FUNCIONAL-CORRECCIONES.md). Notificaciones usa el contrato interno autenticado de Turnos para validar la vigencia de recordatorios; Pacientes conserva identidad, verificación y vínculos.
