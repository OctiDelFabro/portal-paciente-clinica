# portal-paciente-clinica
Portal paciente para una única clínica, dominio aprobado para Arquitectura de Software 2026.
Entrega 2: reserva de turnos de punta a punta (Turnos → RabbitMQ → Notificaciones → correo) con logs correlacionados y trazas distribuidas. Repositorio público [OctiDelFabro/portal-paciente-clinica](https://github.com/OctiDelFabro/portal-paciente-clinica).

## Integrantes

- Octavio Del Fabro
- Andres Romanutti
- Matias Yelicich

Comisión: pendiente de completar.

## Flujo principal y alcance
Un paciente se registra y recepción verifica sus datos antes de habilitarlo. Busca médicos/especialidades y reserva un turno disponible con al menos 24 h de anticipación. Se evitan superposiciones del médico y del paciente. La reserva genera aviso interno y correo; el recordatorio se prevé 24 h antes.

Paciente y recepción cancelan/reprograman con al menos 24 h respecto del turno original; el nuevo horario también respeta esa anticipación. Una reprogramación fallida mantiene el turno original y lo informa. Los responsables verificados gestionan turnos e historia clínica de menores.
El médico define agenda y duración; los cambios de duración preservan las reservas existentes. Un bloqueo puede cancelar turnos incluso dentro de las últimas 24 h y genera avisos. Recepción no accede al contenido clínico; un turno cancelado por sí solo no habilita el acceso médico.

## Arquitectura y documentación
Frontend React/TypeScript; gateway y cinco servicios Go. Turnos, Pacientes, Médicos y Notificaciones usan MySQL en el diseño; Historia clínica usa MongoDB. RabbitMQ comunica eventos, OpenSearch proyecta perfiles y Redis se prevé para lectura a justificar.
- [Alcance y criterios](docs/SPEC.md).
- [Arquitectura](docs/ARCHITECTURE.md) y [diagramas](docs/diagrams/README.md).
- ADRs: [D1 límites](docs/adr/ADR-001-Limites-de-servicios.md), [D2 arquitectura interna](docs/adr/ADR-002-Arquitectura-interna.md), [D3 persistencia](docs/adr/ADR-003-Persistencia.md), [D5 comunicación](docs/adr/ADR-005-Comunicacion.md), [D6 búsqueda](docs/adr/ADR-006-Busqueda.md), [D7 caché](docs/adr/ADR-007-Cache.md), [D8 contrato propio](docs/adr/ADR-008-Contrato-propio.md), [D9 proveedor](docs/adr/ADR-009-Consumo-proveedor.md), [D10 resiliencia](docs/adr/ADR-010-Resiliencia.md), [D11 observabilidad](docs/adr/ADR-011-Observabilidad.md), [D12 balanceo](docs/adr/ADR-012-Balanceo-de-carga.md), [D13 capacidad y costos](docs/adr/ADR-013-Capacidad-y-costos.md).
- [Contrato, errores y ejemplos](docs/contracts/README.md).
- Estado por entrega: [Entrega 2](docs/ENTREGA-2.md) · [Entrega 1](docs/ENTREGA-1.md).
- [Validación](docs/VERIFICACION.md).

## Capacidad compartida
**Turnos para socios**, consumida por Car Cruds (concesionaria) para los chequeos de sus empleados: reserva, consulta y cancelación de turnos con clave por consumidor, idempotencia y facturación por reserva. Guía para el consumidor: [docs/contracts/README.md](docs/contracts/README.md). La API de correos (Notificaciones) sigue disponible, sin consumidor asignado. El proveedor externo que consumirá el portal sigue pendiente. La identidad externa no sustituye la integración con otro grupo.

## Puesta en marcha local
Requisitos: Docker con Compose. Para desarrollo sin contenedores: Go 1.26 o posterior.

1. Copiar `.env.example` a `.env` y completar **todas** estas variables (valores solo locales, nunca reales):
   `MYSQL_ROOT_PASSWORD`, `TURNOS_DB_PASSWORD`, `NOTIF_DB_PASSWORD`, `RABBITMQ_PASSWORD` (sin `@`, `:` ni `/`, porque va dentro de una URL), `MONGO_ROOT_PASSWORD`, `GRAFANA_ADMIN_PASSWORD`, `NOTIF_API_KEYS` (formato `consumidor:clave`) y `MOCK_API_KEYS`.
2. `docker compose up --build`
3. Reservar un turno a través del gateway (el turno debe estar al menos 24 h en el futuro):
```powershell
$start = (Get-Date).ToUniversalTime().AddDays(3).ToString("yyyy-MM-ddT10:00:00Z")
$body = @{ patientId = 'pat-001'; doctorId = 'doc-001'; startAt = $start } | ConvertTo-Json
Invoke-RestMethod http://127.0.0.1:8080/api/turnos/appointments -Method Post -ContentType application/json -Body $body -Headers @{ 'X-Correlation-ID' = 'demo-1' }
```
4. Ver el resultado:

| Qué | Dónde |
|---|---|
| Correo de confirmación | Mailpit: http://localhost:8025 |
| Traza gateway → turnos → RabbitMQ → notifications | Jaeger: http://localhost:16686 (buscar por servicio `gateway`) |
| Tablero de estado | Grafana: http://localhost:3000 |
| Métricas crudas | Prometheus: http://localhost:9090 |
| Colas y DLQ | RabbitMQ: http://localhost:15672 |
| Logs con el mismo `correlation_id` | `docker compose logs turnos notifications gateway` |

Datos semilla mientras Pacientes y Médicos no están operativos: médicos `doc-001` (30 min) y `doc-002` (20 min); pacientes `pat-001`, `pat-002` (no habilitado por recepción) y `pat-003` (menor sin correo, se avisa al responsable).
API de Turnos: `POST /api/turnos/appointments`, `GET /api/turnos/appointments/{id}` y `POST /api/turnos/appointments/{id}/cancel`. Capacidad pública: `/v1/...` por el mismo gateway ([contrato](docs/contracts/README.md)).

**Pruebas**
- Unitarias: `go test ./...`
- Integración contra MySQL y RabbitMQ reales (requiere Docker): `go test -tags integration ./...`
- Contrato de la capacidad publicada (con el stack levantado y `TURNOS_PARTNER_KEYS` definida; usa el puerto público `8088`): `python scripts/check_turnos_contract.py`
- Contrato del mock de Notificaciones: `python scripts/check_contract.py`

El mock en memoria sigue disponible para el grupo consumidor con `docker compose --profile mock up notifications-mock` (puerto 8086). Pacientes, Médicos e Historia clínica siguen como esqueletos con `/healthz`. Frontend: ver `frontend/README.md`.
No existe despliegue público verificado ni URL de producción.

### Si cambiás contraseñas del `.env`
Las contraseñas se aplican cuando se **crean** los contenedores y volúmenes, no cada vez que se levantan:
- **MySQL** crea los usuarios `turnos` y `notificaciones` solo al inicializar un volumen vacío (`infra/mysql-users.sh`). Con un volumen existente, una contraseña nueva produce `Access denied for user 'notificaciones'`.
- **RabbitMQ** toma `RABBITMQ_PASSWORD` al crear su contenedor. Si se recrea con otra contraseña, los servicios que siguen corriendo con la anterior reciben `username or password not allowed`.

Después de cambiar cualquier valor del `.env`, arrancar de cero (borra los datos locales de prueba):
```powershell
docker compose down -v
docker compose up --build -d
```

### Problemas frecuentes
| Síntoma | Causa | Solución |
|---|---|---|
| `notifications` en estado `Exited (1)` y log `NOTIF_API_KEYS=consumer:key is required` | `NOTIF_API_KEYS` sin el formato `consumidor:clave` | Corregir el `.env` (ej. `grupo-demo:clave-demo`) y `docker compose up -d notifications` |
| `notifications` en `Exited (1)` y log `Access denied for user 'notificaciones'` | Contraseña de MySQL cambiada con un volumen existente | `docker compose down -v` y volver a levantar |
| La reserva responde 201 pero no llega el correo a Mailpit | Notificaciones caído o sin cola: el evento queda pendiente en la outbox de Turnos | Ver `docker compose ps` y `docker compose logs notifications`. Al levantarlo, el correo llega solo, sin reservar de nuevo |
| No se puede entrar a RabbitMQ (http://localhost:15672) | El usuario es `localuser` (no `guest`) y la contraseña es `RABBITMQ_PASSWORD` del momento en que se creó el contenedor | Usar esas credenciales, o recrear con `docker compose up -d --force-recreate rabbitmq turnos notifications` |

## Trabajo en equipo
Propuesta inicial: main para versiones revisadas; ramas feature/... y PR con revisión de otro integrante antes de integrar. Adjuntar pruebas pertinentes. El grupo deberá acordar responsables y aplicar esta estrategia al crear el repositorio público.
No subir claves reales, datos de pacientes ni .env. Las muestras usan dominios example.com.

