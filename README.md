# portal-paciente-clinica
Portal paciente para una única clínica, dominio aprobado para Arquitectura de Software 2026.
Entrega 1: diseño inicial, contrato HTTP y mock. Repositorio público creado: [OctiDelFabro/portal-paciente-clinica](https://github.com/OctiDelFabro/portal-paciente-clinica). La publicación del contenido completo sigue pendiente. Integrantes y comisión pendientes de completar.

## Flujo principal y alcance
Un paciente se registra y recepción verifica sus datos antes de habilitarlo. Busca médicos/especialidades y reserva un turno disponible con al menos 24 h de anticipación. Se evitan superposiciones del médico y del paciente. La reserva genera aviso interno y correo; el recordatorio se prevé 24 h antes.

Paciente y recepción cancelan/reprograman con al menos 24 h respecto del turno original; el nuevo horario también respeta esa anticipación. Una reprogramación fallida mantiene el turno original y lo informa. Los responsables verificados gestionan turnos e historia clínica de menores.
El médico define agenda y duración; los cambios de duración preservan las reservas existentes. Un bloqueo puede cancelar turnos incluso dentro de las últimas 24 h y genera avisos. Recepción no accede al contenido clínico; un turno cancelado por sí solo no habilita el acceso médico.

## Arquitectura y documentación
Frontend React/TypeScript; gateway y cinco servicios Go. Turnos, Pacientes, Médicos y Notificaciones usan MySQL en el diseño; Historia clínica usa MongoDB. RabbitMQ comunica eventos, OpenSearch proyecta perfiles y Redis se prevé para lectura a justificar.
- [Alcance y criterios](docs/SPEC.md).
- [Arquitectura](docs/ARCHITECTURE.md) y [diagramas](docs/diagrams/README.md).
- [ADR D1](docs/adr/ADR-001-Limites-de-servicios.md), [D3](docs/adr/ADR-003-Persistencia.md), [D5](docs/adr/ADR-005-Comunicacion.md) y [D8](docs/adr/ADR-008-Contrato-propio.md).
- [Contrato, errores y ejemplos](docs/contracts/README.md).
- [Estado y pendientes](docs/ENTREGA-1.md).
- [Validación](docs/VERIFICACION.md).

## Capacidad compartida
Solo correo por plantillas generales predefinidas. API con clave por consumidor, idempotencia y consulta de estado. El grupo consumidor, el proveedor externo y la capacidad que consumirá el portal siguen pendientes de asignación. La identidad externa no sustituye la integración con otro grupo.

## Ejecución disponible
Go 1.23 o posterior y, para las comprobaciones HTTP, Python 3.10 o posterior.
Desde la raíz en PowerShell:
```powershell
$env:MOCK_API_KEYS = 'grupo-demo:solo-demo-local'
.\scripts\run-mock.ps1
```
Mock: http://127.0.0.1:8085, GET /healthz. Ejemplos en docs/contracts/README.md.
En otra terminal, el gateway inicial se ejecuta con go run ./gateway/cmd y publica la ruta /v1/ en http://127.0.0.1:8080. Esa ruta conserva la clave del consumidor; la autenticación de usuarios del portal está pendiente.
Pruebas: go test -v ./services/notifications/mock y python scripts/check_contract.py.

Los otros servicios tienen únicamente /healthz y responden 501 a operaciones de dominio:
go run ./services/turnos/cmd, ./services/pacientes/cmd, ./services/medicos/cmd o ./services/historia-clinica/cmd.
Los puertos locales son 8081–8084 en ese orden.
Frontend: ver frontend/README.md. Su instalación y build todavía no fueron verificados.

## Entorno completo propuesto
Copiar .env.example a .env y reemplazar valores. Con Docker operativo: docker compose up --build.
Se inicia la estructura backend y dependencias locales; faltan conexiones reales, migraciones, frontend integrado y reglas clínicas. compose.yaml se entrega como configuración inicial sin ejecución acreditada en esta sesión.
No existe despliegue público verificado ni URL de producción.

## Trabajo en equipo
Propuesta inicial: main para versiones revisadas; ramas feature/... y PR con revisión de otro integrante antes de integrar. Adjuntar pruebas pertinentes. El grupo deberá acordar responsables y aplicar esta estrategia al crear el repositorio público.
No subir claves reales, datos de pacientes ni .env. Las muestras usan dominios example.com.

