# Correcciones de la auditoría funcional del 11/11/2026

Base: `main` en `f9f228e` (PR #11). Rama: `fix/auditoria-funcional`. El informe de auditoría describía resultados de producción y funciones ausentes; este documento describe el código y las pruebas añadidas. No acredita el despliegue de estos cambios en el VPS.

## Trazabilidad

| Hallazgo | Cambio |
|---|---|
| N01, JSON sobrante en Médicos | El decoder exige EOF después de un único valor. JSON concatenado o basura devuelve 400 antes de editar. |
| N02, recepción sin gestión de turnos | Reserva, consulta, cancelación, reprogramación y agenda por profesional; filtros de paciente y médico se combinan con AND. Se conserva la prohibición de leer contenido clínico. |
| N03, turnos de menores | Selector propio/menor vinculado; listado, reserva, cancelación, reprogramación, historia y avisos para el objetivo elegido. El backend consulta habilitación, vínculo y edades actuales. |
| N04, DNI repetido | Documento normalizado con trim/mayúsculas. Un bloqueo transaccional por documento serializa altas/ediciones/habilitaciones; conflicto 409 `DOCUMENT_TAKEN`. También aplica a fichas sin correo creadas por recepción. |
| N05, médico que no acepta menores | Se transportan nacimiento/rol y `acceptsMinors` a la validación compartida de reservas del portal y socios. Rechazo 422. |
| RF-04 | Reprogramación en una transacción: cancela el original, crea el reemplazo y ambos eventos. Cualquier error revierte todo. Original y destino requieren 24 h; reemplazo conserva paciente/médico y usa duración/tarifa actuales. |
| RF-06/RF-07 | Horarios semanales/duración persistidos en Turnos; edición propia del médico. Bloqueo cancela las reservas superpuestas, incluso dentro de 24 h, y genera sus eventos en la misma transacción. Levantar un bloqueo no restaura turnos cancelados. |
| RF-11 | Recepción registra y edita nombre, correo opcional, DNI y nacimiento; acceso inicial opcional con correo/contraseña. Verificación y vínculo siguen siendo decisiones explícitas. Búsqueda de pacientes por nombre/DNI/correo. |
| RF-12 | Dos réplicas pueden generar recordatorios sin duplicar marcador/outbox. Notificaciones comprueba la reserva al consumir y antes de enviar un recordatorio. Avisos internos durables aun si falla la consulta de contactos; bandeja paginada y marca de lectura por dueño/responsable. |
| Cotización fuera de agenda | Quote verifica agenda, anticipación, bloqueos y ocupación del médico. Sigue siendo una estimación, sin reservar ni garantizar ausencia de conflicto del paciente. |
| Notas anticipadas | Backend rechaza notas de encuentros futuros con 422 `APPOINTMENT_NOT_STARTED`; botón de atención deshabilitado hasta el inicio. |
| Selector de fechas | 60 días desde mañana en hora de Argentina, incluidos fines de semana configurados; fecha inicial sugerida por próximo libre. |
| Orden entre páginas | El portal obtiene todos los resultados del filtro (máximo 1000), consulta Turnos con cuatro solicitudes concurrentes, ordena globalmente y recién pagina de a ocho. Errores de disponibilidad se muestran como desconocidos. |
| Mensaje de habilitación | Banner de cuenta pendiente, actualización de estado y error clínico `PATIENT_NOT_ENABLED`. |
| Métricas públicas | Rutas de métricas de servicios por `/api` requieren JWT admin. Encabezados de rol falsificados siguen descartándose en gateway. |
| Caché del documento HTML | Nginx sirve `index.html` con revalidación; assets con hash siguen inmutables. |

## Reglas y compatibilidad

La edad se calcula por fecha civil de Argentina. Al cumplir 18 años cesa el acceso del responsable; no se borran las relaciones históricas. La recepción verifica documentos fuera del sistema antes de habilitar o vincular; no hay un servicio externo de identidad ni recuperación automática de cuentas.

La migración de DNI no borra ni elige entre duplicados anteriores. Añade clave normalizada e índice no único, más la tabla de bloqueos para proteger nuevas escrituras. Recepción debe resolver duplicados preexistentes mediante edición de DNI y verificación; no se pueden habilitar ni guardar manteniendo un DNI en conflicto. Se conservan IDs, turnos e historias. Esto permite iniciar sobre datos anteriores sin una migración destructiva ni un índice único que impida el arranque.

Cambiar horarios no puede dejar una reserva vigente fuera de la nueva ventana. Se rechaza con 409; el médico puede cancelar explícitamente mediante un bloqueo. La duración anterior permanece en cada reserva. Horarios entre 00:00 y 24:00, duración de 5–240 minutos, días semanales sin duplicados; no hay ventanas que crucen medianoche. Un bloqueo empieza en el futuro y dura hasta 366 días. Su listado muestra bloqueos vigentes/próximos durante un año.

Los recordatorios se generan cada minuto al entrar en las últimas 24 horas. Después de una caída se recuperan los pendientes cuyo turno aún no empezó; no se prometen recordatorios enviados exactamente al segundo. La transacción garantiza una entrada outbox por reserva, no entrega SMTP exactamente una vez: un correo aceptado por SMTP justo antes de un fallo del proceso puede repetirse. Un envío que ya está en curso cuando se cancela no se puede retirar. El portal no revierte cancelaciones por fallas de correo.

La caché conserva TTL 30 s. Ediciones de agenda, bloqueos y reprogramaciones invalidan los días afectados después del commit. Una lectura concurrente puede reinsertar un dato anterior hasta el TTL; la escritura siempre revalida agenda, bloques y ocupación bajo bloqueo SQL. Próximo libre se calcula desde Turnos sin usar la proyección aproximada del índice. Una franja puede ocuparse entre búsqueda y confirmación.

## Contrato del portal

Todas estas rutas pasan por JWT en el gateway. Prefijos: `/api/turnos`, `/api/pacientes`, `/api/notifications`.

| Método y ruta del servicio | Acceso / cuerpo |
|---|---|
| `GET /appointments?page=1&doctorId=…&patientId=…` | Paciente: propio/menor; médico: propia agenda; recepción/admin: agendas. Filtros opcionales. |
| `POST /appointments` | Paciente propio/menor o recepción/admin; `{patientId,doctorId,startAt}`. |
| `GET /appointments/{id}`, `POST /appointments/{id}/cancel` | Dueño/responsable, recepción/admin o médico asignado. Cancelación común respeta 24 h. |
| `POST /appointments/{id}/reschedule` | Dueño/responsable o recepción/admin; `{startAt}` RFC3339. Devuelve 201 con nuevo ID. |
| `GET /doctors/{id}/schedule` | Médico propio o recepción/admin. |
| `PUT /doctors/{id}/schedule` | Médico propio; `{durationMinutes,weekdays,open,close}`; domingos=0. |
| `GET /doctors/{id}/blocks` | Médico propio o recepción/admin. |
| `POST /doctors/{id}/blocks` | Médico propio; `{startAt,endAt,reason}` RFC3339. |
| `POST /doctors/{id}/blocks/{block}/release` | Médico propio. |
| `GET /dependents` | Paciente adulto habilitado; menores actuales habilitados. |
| `GET /patients?page=1&q=…` | Recepción/admin; pacientes de a 50. |
| `POST /patients` | Recepción/admin; `{fullName,documentNumber,birthDate,email?,password?,enabled}`. |
| `PUT /patients/{id}` | Recepción/admin; `{fullName,documentNumber,birthDate,email?}`. No modifica rol ni contraseña. |
| `GET /notices?page=1&patientId=…` | Paciente propio/menor; 50 por página, `nextPage=0` al terminar. |
| `POST /notices/{id}/read?patientId=…` | Mismo permiso, marca idempotente; un aviso ajeno devuelve 404. |

`GET /internal/appointments/{id}` requiere `X-Service-Key` de 32 bytes o más para Notificaciones. Gateway no expone `/internal/`. Los eventos nuevos son `turnos.reprogramado.v1` y `turnos.recordatorio.v1`. La cancelación del original de una reprogramación lleva `data.operation="reschedule"` para que Notificaciones no emita un segundo aviso; el texto libre del motivo no controla esa decisión. Historia e índice reciben la nueva reserva y la cancelación anterior.

## Verificación

Unitarias: permisos y revocación del responsable, rechazo de menores, reprogramación con conflicto/destino inválido, conservación de original/outbox, eventos de reemplazo, notas futuras, EOF JSON, métricas restringidas y validación HTTP del recordatorio. Cliente: serialización de filtros/objetivos y descarga del catálogo completo antes de paginar.

Integración MySQL: unicidad concurrente y reparación de DNI anterior, agenda persistida/duración conservada, conflicto de horarios, reprogramación y rollback, bloqueo contra reserva concurrente, cancelación excepcional, levantamiento sin resurrección, dos generadores de recordatorios, aviso durable con directorio caído, replay sin duplicar, acceso de responsable y descarte de correo obsoleto. Se ejecutan con el resto de pruebas reales de RabbitMQ, MongoDB, OpenSearch y Redis.

Chromium recorre la SPA compilada con HTTP simulado: paciente pendiente/habilitado, reserva/reprogramación/cancelación, menor, avisos, agenda/notas/horarios/bloqueos del médico, edición/asignación por recepción y perfil administrativo. Incluye viewport de 390 px. Estas pruebas no equivalen a una auditoría del VPS.

En el entorno de edición pasaron Go con detector de carreras, `go vet`, tests de cliente y build TypeScript/Vite; las pruebas de integración compilan. No hay Docker y Chromium está bloqueado por permisos de socket. El resultado de ejecución del navegador y la infraestructura se verifica en los jobs del PR. No se omiten tests por esas limitaciones locales.

## Despliegue a cargo del equipo

Esta entrega no modifica el VPS. El responsable del despliegue debe actualizar también **Pacientes, Médicos, Historia Clínica y Notificaciones**, además de las dos réplicas de Turnos, gateway y frontend. El comando del parche anterior limitado a Turnos/gateway ya no alcanza:

```sh
git pull --ff-only origin main
docker compose -f compose.yaml -f compose.hostinger.yaml up -d --build pacientes medicos historia-clinica notifications turnos-1 turnos-2 gateway
cd frontend
npm ci
npm run build
```

Publicar `frontend/dist` en el nginx del portal y aplicar `infra/nginx-portal.conf`. Mantener `INTERNAL_API_KEY` actual, compartida por servicios; Notificaciones consulta `TURNOS_URLS` con los nombres internos de Compose por defecto. Las migraciones se aplican al iniciar servicios. Conservar respaldos y volúmenes; no ejecutar `down -v`. Actualizar productores y consumidores durante una ventana coordinada para que los consumidores nuevos estén disponibles antes de utilizar reprogramaciones/recordatorios.

Después, repetir los flujos de paciente, responsable, médico y recepción en el sitio, y verificar los correos con el proveedor configurado. Las mediciones de carga, failover y el acuerdo con el proveedor externo siguen siendo validación operativa pendiente, no defectos que puedan darse por corregidos por compilar.
