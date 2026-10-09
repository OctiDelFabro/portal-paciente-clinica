# portal-paciente-clinica — Arquitectura
docs/ARCHITECTURE.md · Versión inicial para la Entrega 1 · Diseño preliminar
Este documento describe la arquitectura propuesta a partir del alcance confirmado y los diagramas existentes. No acredita componentes implementados ni desplegados. Las decisiones abiertas se indican al final.
## 1. Contexto y alcance
El portal atenderá a una única clínica. Los actores son paciente, médico y recepcionista. El paciente gestiona turnos propios y de menores a cargo y consulta historias clínicas; el médico configura su agenda, consulta sus turnos y registra atenciones; recepción registra pacientes, gestiona turnos y verifica vínculos con menores.
El grupo ofrecerá a otro grupo la capacidad de envío de correos por plantillas generales. Las notificaciones dentro del portal son de uso interno. El grupo proveedor y la capacidad externa que consumirá el portal todavía no están definidos. El proveedor de identidad de terceros es una dependencia técnica y no reemplaza esa integración entre grupos.
## 2. Componentes y distribución
Frontend web — React y TypeScript
Centralizará las pantallas y acciones de pacientes, médicos y recepción. Consumirá el API Gateway para los flujos del portal. No accederá a bases de datos ni al broker de mensajería y no iniciará la integración con el proveedor asignado a otro grupo.
API Gateway — Go y Gin
Será el punto de entrada de la interfaz web. Validará las credenciales según el proveedor de identidad elegido, aplicará autorización y direccionará las solicitudes a los microservicios. La autenticación, el control de tráfico y la distribución se concretarán por etapas. El dominio y sus reglas permanecerán en los servicios responsables.
Turnos — Go; almacenamiento MySQL
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
Notificaciones — Go; almacenamiento MySQL
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
El microservicio responsable del grupo consumidor llamará a la URL publicada de Notificaciones. La petición identificará una plantilla, un destinatario y sus parámetros. La capacidad registrará la solicitud, devolverá un identificador y procesará el correo de forma asíncrona. El contrato local define X-API-Key por consumidor, las tres operaciones HTTP, campos, idempotencia y consulta de estados. La URL pública y el proveedor de correo siguen pendientes.
## 5. Comunicación y dependencias
Se utilizará comunicación HTTP para solicitudes del frontend a través del gateway, consultas entre servicios y la capacidad pública. La configuración de timeout, errores y reintentos se definirá por dependencia; las operaciones que puedan repetirse necesitarán una política de idempotencia.
RabbitMQ transportará los eventos internos que Notificaciones consuma. Se prevén eventos de reserva, cancelación y reprogramación de turnos. Sus nombres, esquemas, versiones, confirmaciones, reintentos y tratamiento de mensajes fallidos se documentarán en D5 y en los contratos internos.
## 6. Diagramas
La carpeta de la Entrega 1 contiene los diagramas de contexto y contenedores en formato diagrams.net. Deben actualizarse para representar el frontend React/TypeScript, la propiedad de la agenda en Turnos, las consultas entre servicios, el consumidor externo de correos y el proveedor de correo aún pendiente.
El diagrama de contexto deberá conservar los tres actores y la autenticación externa y distinguir la capacidad ofrecida de la capacidad externa todavía no asignada. El de contenedores deberá identificar los cinco microservicios, sus bases, el gateway, el frontend, RabbitMQ, OpenSearch y Redis.
## 7. Estado de puesta en marcha y decisiones abiertas
El proyecto y el repositorio se llaman portal-paciente-clinica. El repositorio público ya fue creado en https://github.com/OctiDelFabro/portal-paciente-clinica y se verificó su rama main. La carga del contenido completo sigue pendiente. Este paquete incorpora estructura inicial y comandos para ejecutar el mock y los esqueletos; las funcionalidades clínicas y las conexiones reales a dependencias aún no se implementaron.
Pendientes: publicación del contenido completo en el repositorio; proveedor de autenticación y mecanismo técnico de registro; datos requeridos para la validación por recepción; edición y permisos de historia clínica aún abiertos; proveedor de correo; implementación de contratos; traslado de cambios a los diagramas originales de Drive; mecanismo de concurrencia y publicación fiable; implementación de servicios; arquitectura interna y ejecución local completa.
Las pruebas, observabilidad, resiliencia, búsqueda, caché y balanceo se desarrollarán en las etapas correspondientes. Su presencia en el diseño no acredita una implementación operativa. D1 y D8 y las versiones iniciales de D3 y D5 deberán estar disponibles para la Entrega 1.
## 8. Actualización de este paquete
Las reservas existentes conservan horario y duración cuando el médico cambia la duración; el cambio se aplica a nuevas reservas. Los esquemas y los diagramas Mermaid adjuntos representan el diseño actualizado; los originales diagrams.net de Drive aún deben ajustarse. D3 y D5 ya tienen primera versión y D8 documenta el contrato HTTP.

