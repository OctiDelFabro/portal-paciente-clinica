# Portal Paciente Clínica

Proyecto de Arquitectura de Software 2026 de Octavio Del Fabro, Andres Romanutti y Matias Yelicich. React/TypeScript, gateway y cinco servicios Go: Turnos, Pacientes, Médicos, Historia Clínica y Notificaciones.

## Qué funciona

- Registro, login y JWT de paciente, médico, recepción y administración. Las cuentas nuevas quedan pendientes de verificación; autenticarse no las habilita para reservar o consultar historias clínicas.
- Búsqueda de profesionales por nombre o especialidad, filtros, detalle y disponibilidad.
- Reserva, cancelación y reprogramación propias, de menores vinculados y por recepción con 24 h de anticipación, agenda de atención en ART (UTC-03:00) y prevención de superposiciones por médico y paciente.
- Listados paginados, próximos turnos activos primero; la interfaz permite cargar más resultados.
- Agenda profesional, notas del médico asignado y lectura de historia clínica con un turno vigente o pasado no cancelado. El paciente habilitado consulta su propia historia. Existe un endpoint para el responsable verificado de un menor; recepción no accede al contenido clínico.
- Recepción y administración pueden listar/verificar pacientes y registrar el vínculo con un adulto habilitado. Administración edita perfiles y reconstruye la búsqueda.
- Eventos durables de reserva/cancelación, correo a contactos actuales de Pacientes y a responsables válidos, avisos internos persistidos, reintentos y DLQ.
- Estimación energética con proveedor degradable, reestimación y tarifa base/por kWh conservadas desde la reserva.
- API de socios con clave por consumidor, aislamiento, idempotencia y precio persistido.

La auditoría funcional incorpora edición/bloqueo de agenda, gestión de menores y recepción, recordatorios/bandeja, validación de DNI y restricciones de edad/notas. Ver [correcciones, reglas y despliegue](docs/AUDITORIA-FUNCIONAL-CORRECCIONES.md); requiere actualizar todos los servicios implicados y frontend.

## Ejecutar localmente

Requisitos: Docker con Compose; Node.js 22 y npm para React. Para Go sin contenedores: Go 1.26 o posterior.

1. Copiar `.env.example` a `.env`. Completar las contraseñas MySQL raíz y de los cuatro servicios, MongoDB, RabbitMQ y Grafana, `NOTIF_API_KEYS=consumidor:clave`, `JWT_SECRET` e `INTERNAL_API_KEY` (ambas de al menos 32 bytes). Usar valores locales; nunca subir `.env`. Evitar `@`, `:` y `/` en contraseñas insertadas en URLs/DSN. Las claves internas permanecen en el backend.
2. Ejecutar `docker compose up -d --build`.
3. En `frontend/`, ejecutar `npm ci` y `npm run dev`. Abrir http://127.0.0.1:5173. Vite redirige `/api` al gateway local en `8080`.
4. Con `DEMO_PATIENT_PASSWORD` configurada, iniciar sesión con alguna cuenta de la tabla. La misma contraseña local sirve para todas las cuentas demo con correo.

| Cuenta demo | Identidad | Estado/rol |
|---|---|---|
| `paciente1@example.test` | `pat-001` | Paciente habilitado |
| `paciente2@example.test` | `pat-002` | Pendiente de habilitación |
| Sin login | `pat-003` | Menor vinculado a `pat-001` |
| `doctor1@example.test` | `doc-001` | Médico, lunes a viernes 09:00–13:00, 30 min |
| `doctor2@example.test` | `doc-002` | Médico, lunes a viernes 14:00–18:00, 20 min |
| `recepcion@example.test` | `rec-001` | Recepción |
| `admin@example.test` | `admin-001` | Administración |

Sin `DEMO_PATIENT_PASSWORD` se crean datos ficticios, pero no se habilita su login. El registro público siempre crea pacientes, nunca personal.

Para probar con una cuenta nueva: registrarla, intentar reservar (debe responder `403 PATIENT_NOT_ENABLED`), habilitarla desde la cuenta de recepción, reservar un horario ofrecido y comprobar la lista y el correo en Mailpit. No hay que emitir otro JWT después de verificarla.

| Recurso local | Dirección |
|---|---|
| Frontend | http://127.0.0.1:5173 |
| Gateway portal | http://127.0.0.1:8080 |
| Gateway socios (`/v1`, claves requeridas) | http://127.0.0.1:8088 |
| Mailpit (captura correos, no envía a Internet) | http://127.0.0.1:8025 |
| RabbitMQ (`localuser` / contraseña configurada) | http://127.0.0.1:15672 |
| Jaeger / Prometheus / Grafana | http://127.0.0.1:16686 / http://127.0.0.1:9090 / http://127.0.0.1:3000 |

## Pruebas

```sh
go test -race ./...
go vet ./...
# Docker requerido: MySQL, RabbitMQ, MongoDB, Redis y OpenSearch reales
go test -tags integration -p 1 -timeout 15m ./...
cd frontend
npm ci
npm test
npm run build
```

CI ejecuta estas suites en `.github/workflows/verify.yml`. Compilar las pruebas con `-run '^$'` solamente valida compilación; no acredita una integración ejecutada. Ver [evidencia y límites](docs/VERIFICACION.md).

Con el stack local levantado, `python scripts/check_turnos_contract.py` usa `TURNOS_KEY` y opcionalmente `TURNOS_KEY_OTHER`. Crea y cancela una reserva facturada de prueba. El mock de Notificaciones se inicia con `docker compose --profile mock up notifications-mock`; su contrato se verifica con `python scripts/check_contract.py`.

## Configuración y migraciones

El inicio aplica migraciones aditivas de precio y amplía el motivo de cancelación a 200 caracteres. Los turnos anteriores no guardaban el precio base histórico: se fija una vez la tarifa configurada al actualizar. No se puede reconstruir un valor histórico que nunca se almacenó. Hacer copia de los datos antes de actualizar un entorno que se quiera conservar.

Las contraseñas de usuarios de MySQL y RabbitMQ se aplican al inicializar sus volúmenes. Cambiar `.env` no cambia esas credenciales persistidas: actualizar los usuarios con las herramientas del motor y recrear los servicios afectados. `docker compose down -v` borra todos los datos; reservarlo para un entorno de prueba descartable. RabbitMQ ahora conserva su estado en un volumen nombrado; antes de recrear un broker antiguo sin volumen, drenar o respaldar sus mensajes.

## Despliegue y contratos

Se conserva el despliegue de socios de `main`: https://api-turnos.srv1779429.hstgr.cloud/v1, con `/healthz` y `/v1/*`, usando `compose.hostinger.yaml`. El frontend publicado lo administra el equipo; modificar esta rama no actualiza ese servidor. El despliegue debe seguir la [guía de correcciones](docs/AUDITORIA-FUNCIONAL-CORRECCIONES.md#despliegue-a-cargo-del-equipo), sin publicar las rutas internas. El proveedor energético está implementado con el contrato versionado de RenewableEnergy; Compose usa `provider-stub` de forma predeterminada. Una llamada real al otro grupo requiere su URL y clave válidas.

Documentación: [SPEC](docs/SPEC.md), [arquitectura](docs/ARCHITECTURE.md), [ADRs](docs/adr), [contrato socios](docs/contracts/README.md), [contrato Notificaciones](docs/contracts/NOTIFICATIONS.md), [frontend](frontend/README.md), [correcciones de la revisión](docs/CORRECCIONES-REVISION.md).

Trabajar con ramas y PR revisadas antes de integrar en `main`. No subir claves reales ni datos de pacientes.
