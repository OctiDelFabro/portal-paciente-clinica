# ADR-001 — Límites de los servicios
Decisión D1 · Estado: límites preliminares definidos para la Entrega 1
Ubicación prevista en el repositorio: docs/adr/ADR-001-Limites-de-servicios.md.
Contexto
El grupo construirá un portal paciente para una única clínica, con pacientes, médicos y recepcionistas. El dominio incluye restricciones temporales, vínculos entre menores y responsables, permisos de historia clínica y prevención de turnos superpuestos. El enunciado exige al menos tres microservicios y un API Gateway.
Los diagramas existentes proponen cinco servicios. El grupo confirmó que Turnos será responsable de agenda, duración, disponibilidad y reservas, mientras Médicos administrará perfiles y especialidades. La capacidad propia a ofrecer a otro grupo será Notificaciones.
Decisión
Turnos
Será propietario de agendas, horarios de atención, duración de turnos, bloqueos, disponibilidad y reservas, incluyendo cancelación y reprogramación. Aplicará las reglas de anticipación mínima de 24 horas y evitará superposiciones tanto del médico como del paciente. El bloqueo médico podrá cancelar turnos dentro de ese plazo.
Médicos
Será propietario de perfiles de profesionales y especialidades y de la capacidad de búsqueda de esos perfiles. No mantendrá la agenda ni una disponibilidad independiente de Turnos.
Pacientes
Será propietario de los datos de pacientes, sus contactos, su estado de habilitación y los vínculos verificados entre menores y responsables. El paciente podrá registrarse y recepción verificará sus datos antes de habilitarlo. Recepción también registrará y actualizará esos datos mediante su interfaz. La relación técnica entre cuentas de acceso y pacientes se definirá junto con autenticación.
Historia clínica
Será propietario de historias clínicas y registros de atenciones. Aplicará permisos según el rol y las relaciones con el paciente. Un turno cancelado no habilita por sí mismo el acceso médico a la historia clínica. Obtendrá las validaciones necesarias mediante contratos de Pacientes y Turnos, sin consultar directamente sus bases.
Notificaciones
Será propietario del catálogo de plantillas, solicitudes, estados de procesamiento y avisos internos. Consumirá eventos del portal y ofrecerá al grupo consumidor una API HTTP de correos por plantillas. El detalle del contrato se registra en D8.
Relaciones y propiedad de los datos
El frontend React/TypeScript centralizará la interacción y accederá al backend a través del API Gateway Go/Gin. El gateway validará las credenciales y dirigirá solicitudes, mientras las reglas de negocio se ejecutarán en los servicios responsables.
Cada servicio accederá solo a su almacenamiento propio. Las consultas entre servicios se realizarán mediante contratos, y los eventos internos utilizarán RabbitMQ. La búsqueda y la caché serán representaciones derivadas, no fuentes autoritativas para confirmar una reserva.
La persistencia del diseño existente es MySQL para Turnos, Médicos, Pacientes y Notificaciones, y MongoDB para Historia clínica. El uso concreto y sus limitaciones se justificarán en la versión inicial de D3; la separación lógica de datos no obliga a un servidor físico por servicio en desarrollo local.
Justificación
Mantener agenda, disponibilidad y reservas en Turnos evita que la misma regla de ocupación deba coordinar dos autoridades distintas. Turnos puede verificar sus reglas contra datos propios. Médicos concentra información de profesionales que cambia con una frecuencia y un propósito distintos a las reservas.
La gestión de pacientes y responsables concentra identidad de negocio y vínculos utilizados por otros flujos. Historia clínica separa los registros de atención y sus permisos de la administración de datos de contacto. Notificaciones separa los avisos del resultado de la operación de turno y permite publicar una capacidad útil para otro equipo.
Alternativas consideradas
Agenda en Médicos y reservas en Turnos
Es una separación posible, pero exigiría coordinar los cambios de agenda y los bloqueos entre dos servicios para evitar divergencias. El grupo eligió concentrar ambas responsabilidades en Turnos.
Notificaciones dentro de Turnos
Reduciría componentes, pero mezclaría la operación de reserva con el procesamiento y seguimiento de avisos, y dificultaría ofrecer una capacidad independiente al otro grupo. Se mantiene el servicio de Notificaciones separado.
Base de datos común con acceso directo entre servicios
Facilitaría consultas cruzadas iniciales, pero acoplaría los servicios al esquema interno de los demás y debilitaría la propiedad de los datos. Se eligen interfaces explícitas y almacenamiento lógicamente separado.
Consecuencias y limitaciones
Se mantienen cinco microservicios de negocio y un gateway, con contratos y dependencias que deberán gestionarse. Las operaciones que necesiten datos de otro servicio tendrán que definir tiempos de espera y comportamiento ante fallas. La comunicación y los errores parciales se desarrollarán en D5, D4 y las decisiones posteriores de resiliencia.
La publicación de eventos y el registro de reservas deben coordinarse para no perder avisos ni generar efectos duplicados. La estrategia de concurrencia y publicación fiable sigue pendiente de diseño concreto. La división inicial se validará durante la implementación; si cambia, el ADR anterior conservará su historia.
Referencias del proyecto
SPEC.md contiene el alcance y las reglas confirmadas. docs/ARCHITECTURE.md explica los componentes y flujos. ADR-008-Contrato-propio.md desarrolla la capacidad compartida. Los diagramas de contexto y contenedores deben actualizarse para reflejar esta decisión.
