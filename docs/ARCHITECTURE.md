# portal-paciente-clinica — Arquitectura
docs/ARCHITECTURE.md · Versión Entrega 2
Describe la arquitectura del portal. En la Entrega 2 están operativos el gateway, Turnos y Notificaciones con MySQL, RabbitMQ y la pila de observabilidad; el resto se indica como diseño. Las decisiones abiertas se listan al final.
## 1. Contexto y alcance
El portal atenderá a una única clínica. Los actores son paciente, médico y recepcionista. El paciente gestiona turnos propios y de menores a cargo y consulta historias clínicas; el médico configura su agenda, consulta sus turnos y registra atenciones; recepción registra pacientes, gestiona turnos y verifica vínculos con menores.
El grupo ofrece a Car Cruds (otro grupo) la capacidad de **reservar, consultar y cancelar turnos**, con clave por consumidor, idempotencia y facturación por reserva ([contracts/README.md](contracts/README.md), ADR-008). Hasta el 22/10/2026 la capacidad prevista era el envío de correos por plantillas generales. Las notificaciones dentro del portal son de uso interno. El grupo proveedor y la capacidad externa que consumirá el portal todavía no están definidos. El proveedor de identidad de terceros es una dependencia técnica y no reemplaza esa integración entre grupos.
## 2. Componentes y distribución
Frontend web — React y TypeScript
Centralizará las pantallas y acciones de pacientes, médicos y recepción. Consumirá el API Gateway para los flujos del portal. No accederá a bases de datos ni al broker de mensajería y no iniciará la integración con el proveedor asignado a otro grupo.
API Gateway — Go (net/http)
Será el punto de entrada de la interfaz web. Validará las credenciales según el proveedor de identidad elegido, aplicará autorización y direccionará las solicitudes a los microservicios. Implementado: direccionamiento a Turnos (`/api/turnos/*`) y Notificaciones (`/v1/*`), timeouts y un reintento de GET (D5), correlación, trazas y métricas. Se usa la biblioteca estándar en lugar de Gin porque el gateway solo hace proxy y comparte el middleware de observabilidad con los servicios. Pendiente: autenticación, rate limit del portal y balanceo (D12). El dominio y sus reglas permanecerán en los servicios responsables.
Turnos — Go, arquitectura hexagonal; almacenamiento MySQL · **operativo**
Responsabilidades y datos propios: agenda, horarios de atención, duración de turnos por médico, bloqueos, disponibilidad, reservas, cancelaciones y reprogramaciones. Esta asignación de responsabilidades fue confirmada por el grupo. Médicos no mantendrá una segunda agenda autoritativa.
Reglas: mínimo 24 horas de anticipación para reservar, asignar, cancelar y reprogramar; ausencia de superposiciones por médico y paciente; bloqueo médico como excepción al plazo de cancelación. El bloqueo cancela los turnos afectados y genera las notificaciones correspondientes.
Dependencias previstas: MySQL, Pacientes para identidad y vínculos con menores, Médicos para los perfiles de profesionales y RabbitMQ para publicar eventos. Redis se reserva para un flujo de lectura a justificar y medir; una reserva se valida contra el almacenamiento autoritativo, no solo contra caché.
Médicos — Go; almacenamiento MySQL
Responsabilidades y datos propios: perfiles de médicos y especialidades. La agenda, la duración y las reservas pertenecen a Turnos. Gestionará la búsqueda de profesionales mediante OpenSearch, manteniendo MySQL como fuente autoritativa de los datos del perfil.
Dependencias previstas: MySQL y OpenSearch. La actualización del índice, los filtros, el ordenamiento, la paginación y el retraso tolerable se precisarán en la decisión de búsqueda.
Pacientes — Go; almacenamiento MySQL
Responsabilidades y datos propios: datos de pacientes, estado de habilitación del paciente y vínculos verificados entre menores y adultos responsables. El paciente podrá registrarse por su cuenta; recepción verificará sus datos antes de habilitarlo. Recepción también registrará o actualizará pacientes y habilitará vínculos. Los datos de contacto necesarios para notificar se obtendrán a través de su interfaz.
La identidad de acceso y los roles se relacionarán con el proveedor de autenticación, aún por elegir. Crear una identidad de acceso no habilita automáticamente al paciente: Pacientes conserva el estado de verificación por recepción y los servicios comprueban la habilitación para las operaciones protegidas. El mecanismo técnico de registro y su vínculo con la identidad están pendientes. La información de la historia clínica no pertenece a este servicio.
Historia clínica — Go; almacenamiento MongoDB
Responsabilidades y datos propios: historias clínicas y registros de atenciones. Gestionará su consulta y el registro de atenciones por médicos. La ubicación no relacional se justifica en ADR-003-Persistencia.md. Recepción no tiene acceso al contenido clínico.
Aplicará permisos según el rol, los vínculos con menores y la relación entre médico y paciente a través de turnos. Consultará las capacidades necesarias de Pacientes y Turnos mediante sus interfaces, sin acceder directamente a sus bases. Un turno cancelado no habilita el acceso médico por sí mismo; si es el único vínculo, el acceso se rechaza.
Notificaciones — Go, arquitectura en capas; almacenamiento MySQL · **operativo**
Responsabilidades y datos propios: catálogo de plantillas, solicitudes de notificación, estados de procesamiento y avisos del portal. El consumidor externo podrá solicitar únicamente correos con plantillas generales de confirmación, cancelación y recordatorio. El contrato será HTTP, formal y versionado en OpenAPI.
Para el portal consumirá eventos desde RabbitMQ y generará correo y avisos internos por reserva, cancelación, reprogramación y recordatorio 24 horas antes. Los destinatarios de turnos de menores serán el menor y su adulto responsable; si el menor no tiene correo propio, el correo se dirige únicamente al adulto.
Si falla el correo de una cancelación por bloqueo, la cancelación se mantiene, el aviso se publica en el portal y se reintenta el correo. El proveedor de correo está pendiente; los parámetros iniciales de reintento se proponen en D5. La respuesta HTTP 202 del contrato indica aceptación para procesamiento, no confirmación de envío.
## 3. Propiedad de los datos
Cada microservicio será responsable de sus datos y accederá a las capacidades de los demás mediante contratos. Las bases de datos representadas en el diseño son lógicamente independientes. No se implementarán consultas o escrituras directas entre bases de distintos servicios. El despliegue local podrá compartir un servidor MySQL con bases y credenciales separadas, como propuesta de configuración a concretar.
Los índices de OpenSearch y la caché de Redis son representaciones derivadas. Su contenido no reemplaza las garantías del almacenamiento autoritativo. D1 registra los límites de los servicios y D3 justificará la persistencia por servicio.
## 4. Flujos principales
Reserva y reprogramación
El usuario accede al frontend; el gateway dirige la acción a Turnos. Turnos verifica el actor, los vínculos y la disponibilidad necesaria, aplica la anticipación de 24 horas y evita superposiciones. Tras registrar la operación, publica el evento correspondiente para Notificaciones.
La operación crítica es reservar o cambiar una reserva sin dejar turnos superpuestos para el médico o el paciente. El mecanismo de transacciones, concurrencia y publicación fiable se concretará en D4 y D5. Si falla la reprogramación, se conserva el turno original y se informa al paciente; esta regla fue confirmada.
Bloqueo de agenda
El médico solicita el bloqueo a Turnos mediante el frontend y el gateway. Turnos bloquea la agenda y cancela las reservas afectadas, aun cuando falten menos de 24 horas. Notificaciones procesa los avisos. La falla del correo no revierte la cancelación.
Recordatorios
El componente responsable de coordinar el recordatorio se ubicará en Turnos, porque allí residen la fecha y el estado de la reserva. Esta es una propuesta de diseño. Generará la solicitud de aviso 24 horas antes. Deberá evitar avisos obsoletos cuando la reserva cambie o se cancele; el mecanismo de programación está pendiente.
Consulta de historia clínica
El gateway dirige la consulta al servicio de Historia clínica. El servicio verifica el rol y las relaciones que habilitan el acceso mediante contratos de Pacientes y Turnos. Solo después devuelve la información de su almacenamiento.
Capacidad ofrecida a otro grupo
El servicio Empleados de Car Cruds llama a `/v1/appointments` en el puerto público del gateway (`PUBLIC_HTTP_ADDR`, que no atiende las rutas internas `/api/*` ni `/metrics`) con su `X-API-Key` y una `Idempotency-Key` por reserva. El gateway enruta a Turnos, que autentica, aplica el límite, valida las reglas de reserva, registra dueño y precio en `partner_bookings` en la misma transacción que el turno y el evento, y devuelve 201. El consumidor consulta sus turnos y su estado de cuenta (`/v1/usage`). Antes del 23/10/2026: el microservicio responsable del grupo consumidor llamaría a la URL publicada de Notificaciones. La petición identificará una plantilla, un destinatario y sus parámetros. La capacidad registrará la solicitud, devolverá un identificador y procesará el correo de forma asíncrona. El contrato local define X-API-Key por consumidor, las tres operaciones HTTP, campos, idempotencia y consulta de estados. La URL pública y el proveedor de correo siguen pendientes.
## 5. Comunicación y dependencias
Se utilizará comunicación HTTP para solicitudes del frontend a través del gateway, consultas entre servicios y la capacidad pública. La configuración de timeout, errores y reintentos se definirá por dependencia; las operaciones que puedan repetirse necesitarán una política de idempotencia.
RabbitMQ transportará los eventos internos que Notificaciones consuma. Se prevén eventos de reserva, cancelación y reprogramación de turnos. Sus nombres, esquemas, versiones, confirmaciones, reintentos y tratamiento de mensajes fallidos se documentarán en D5 y en los contratos internos.
## 5.1 Observabilidad
Logs JSON correlacionados (`correlation_id`, `trace_id`), métricas Prometheus en `/metrics` de cada servicio, trazas OpenTelemetry exportadas a Jaeger y un tablero Grafana provisionado. El contexto de traza viaja por HTTP, por headers AMQP y se guarda en la outbox y en cada notificación, así la traza cubre también los pasos asíncronos. La justificación de cada métrica y de cada campo de log está en [ADR-011](adr/ADR-011-Observabilidad.md).

## 6. Diagramas
Los diagramas de contexto y de contenedores están en Mermaid en [docs/diagrams](diagrams/README.md). El de contenedores se actualizó en la Entrega 2 con la outbox, la DLQ, Mailpit y la pila de observabilidad. Los originales diagrams.net de Drive siguen sin reflejar estos cambios.
## 7. Estado de puesta en marcha y decisiones abiertas
Operativo en la Entrega 2 (`docker compose up --build`): gateway, Turnos, Notificaciones, MySQL, RabbitMQ, Mailpit, Jaeger, Prometheus y Grafana. Pacientes, Médicos e Historia clínica siguen como esqueletos; Turnos y Notificaciones obtienen sus datos por puertos con datos semilla. El estado detallado está en [ENTREGA-2.md](ENTREGA-2.md).

Decisiones registradas: D1, D2, D3, D5, D6, D7, D8, D9, D10, D11, D12 y D13 en `docs/adr/`.

## 8. Limitaciones conocidas y deuda técnica aceptada
| Limitación | Motivo | Cuándo se resuelve |
|---|---|---|
| Pacientes y Médicos con datos semilla | Priorizar el flujo de punta a punta | Presentación final |
| Sin autenticación de usuarios del portal | Proveedor de identidad pendiente | Presentación final |
| Rate limit de la API pública por instancia | Contador en memoria | Al replicar Notificaciones (mover a Redis) |
| Búsqueda, caché y balanceo solo diseñados | D6, D7 y D12 | Presentación final |
| Sin recordatorios, reprogramación ni bloqueos de agenda | Alcance de la Entrega 2 | Presentación final |
| Correos solo a Mailpit | Proveedor de correo pendiente | Despliegue público |
