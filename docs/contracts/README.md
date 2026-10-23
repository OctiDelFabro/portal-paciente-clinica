# Capacidad publicada: Turnos para socios v1
Permite que un sistema de otro grupo **reserve, consulte y cancele turnos médicos** en la clínica (por ejemplo, el chequeo preocupacional de un empleado) y **consulte cuánto se le cobra**. Cada reserva exitosa se factura.

- Contrato formal: [turnos-partner-v1.openapi.json](turnos-partner-v1.openapi.json), OpenAPI 3.1.1, versión de API **1.0.0**.
- Decisión y justificación: [ADR-008](../adr/ADR-008-Contrato-propio.md).
- Consumidor asignado: **Car Cruds** (concesionaria, servicio Empleados).
- Estado: operativo en el entorno local con `docker compose`; la URL pública se comunica a cada consumidor junto con su clave.

## Camino rápido
Todas las llamadas salen del **backend** del consumidor (nunca del frontend) y llevan `X-API-Key`.

1. **Elegir médico:** `GET /v1/doctors` → `{"doctors":[{"id":"doc-001","durationMinutes":30}, ...]}`.
2. **Reservar** con una `Idempotency-Key` propia, generada y guardada **antes** de llamar:
   ```http
   POST /v1/appointments
   X-API-Key: <clave del consumidor>
   Idempotency-Key: carcruds-revision-personal-001
   Content-Type: application/json

   {"patientId":"pat-001","doctorId":"doc-001","startAt":"2026-12-01T10:00:00Z"}
   ```
   Respuesta `201`, `Location: /v1/appointments/{id}`:
   ```json
   {"id":"7c1f…","patientId":"pat-001","doctorId":"doc-001","startAt":"2026-12-01T10:00:00Z","endAt":"2026-12-01T10:30:00Z","status":"booked","version":1}
   ```
3. **Seguir el turno:** `GET /v1/appointments/{id}`. Si la clínica lo cancela, `status` pasa a `cancelled` y `version` aumenta.
4. **Ver lo facturado:** `GET /v1/usage?period=2026-12`.

> ¿Timeout o 503 al reservar? **Repetir exactamente el mismo POST con la misma `Idempotency-Key`.** Si el turno se había creado, se recibe el mismo turno con `201` e `Idempotency-Replayed: true`, sin duplicar la reserva ni el cobro.

## Operaciones
| Método y ruta | Uso | Éxito |
|---|---|---|
| `GET /v1/doctors` | Médicos y duración de su turno. | 200 |
| `POST /v1/appointments` | Reservar (se factura). Requiere `Idempotency-Key`. | 201 |
| `GET /v1/appointments/{id}` | Estado actual de un turno **propio**. | 200 |
| `POST /v1/appointments/{id}/cancel` | Cancelar un turno propio, sin cuerpo, hasta 24 h antes. | 200 |
| `GET /v1/usage?period=AAAA-MM` | Estado de cuenta del mes (UTC); sin `period`, el mes actual. | 200 |

Reglas de negocio de la reserva (las mismas que usa el portal):
- `startAt` en RFC 3339 con zona, **al menos 24 h** en el futuro. El fin es `startAt` + duración del médico.
- El paciente debe estar **habilitado por recepción**.
- No puede superponerse con otro turno activo del médico **ni del paciente**.

## Autenticación y uso
| Tema | Regla |
|---|---|
| Credencial | `X-API-Key`, una por grupo consumidor, entregada en privado. Se guarda en la configuración del backend, nunca en Git ni en un frontend. |
| Aislamiento | Un consumidor solo ve, cancela y factura **sus** turnos. Un turno ajeno o inexistente responde lo mismo: `404 NOT_FOUND`. |
| Límite | 60 solicitudes por minuto por consumidor (configurable). Al excederlo: `429` con `Retry-After` en segundos. |
| Transporte | HTTPS en el despliegue compartido. No seguir redirecciones. |
| Correlación | Opcional: enviar `X-Correlation-ID`; el portal lo conserva en logs y trazas, útil para pedir soporte. |
| Caché | Todas las respuestas llevan `Cache-Control: no-store`. |

## Idempotencia
- `Idempotency-Key` es **obligatoria** en `POST /v1/appointments`: de 1 a 128 caracteres `A-Z a-z 0-9 . _ : -`.
- Una clave identifica **una reserva lógica** del consumidor, de forma permanente. Usar una clave nueva por cada turno que se quiere pedir.

| Situación | Resultado |
|---|---|
| Misma clave + mismos datos | `201` con el **mismo** turno, `Idempotency-Replayed: true`. No se vuelve a cobrar. El cuerpo refleja el estado actual del turno. |
| Misma clave + otros datos | `409 IDEMPOTENCY_CONFLICT`. No cambiar la clave para "forzar" otra reserva sin entender por qué. |
| La reserva fue rechazada (409/422/403/404) | El rechazo **no** queda asociado a la clave: corregir los datos y reintentar con la misma clave. |
| Solicitudes simultáneas con la misma clave | Se crea un único turno; todas reciben ese turno. |

"Mismos datos" compara `patientId`, `doctorId` y el **instante** de `startAt`: `2026-12-01T07:00:00-03:00` y `2026-12-01T10:00:00Z` son la misma solicitud.

## Facturación
| Regla | Detalle |
|---|---|
| Qué se cobra | Cada reserva creada con éxito (`201` que no es repetición). |
| Qué no se cobra | Repeticiones idempotentes, rechazos, errores, consultas y cancelaciones. |
| Cancelación | **No** reintegra el cargo: la reserva ya ocupó la agenda del médico. |
| Precio | Precio unitario vigente al reservar, guardado con cada reserva. Un cambio de precio no modifica reservas anteriores. Inicial: **ARS 25.000,00** por reserva (a confirmar con el consumidor). |
| Montos | Enteros en centavos (`2500000` = 25.000,00) con moneda ISO 4217, para evitar errores de redondeo. |
| Período | Mes calendario en UTC, según la fecha de creación de la reserva (`from` inclusive, `to` exclusivo). |

Ejemplo de `GET /v1/usage?period=2026-12`:
```json
{
  "consumer": "carcruds",
  "period": "2026-12",
  "from": "2026-12-01T00:00:00Z",
  "to": "2027-01-01T00:00:00Z",
  "currentPrice": {"amountCents": 2500000, "currency": "ARS"},
  "totals": [{"currency": "ARS", "bookings": 2, "amountCents": 5000000}],
  "items": [
    {"appointmentId": "7c1f…", "bookedAt": "2026-12-01T13:05:12Z", "priceCents": 2500000, "currency": "ARS", "status": "booked"},
    {"appointmentId": "9a02…", "bookedAt": "2026-12-03T09:41:30Z", "priceCents": 2500000, "currency": "ARS", "status": "cancelled"}
  ]
}
```
`totals` agrupa por moneda. `items` permite conciliar cada cargo con el turno que lo originó.

## Errores
Formato: `{"error":{"code":"…","message":"…"}}`. Decidir por `code`, no por `message`.

| HTTP | code | Qué hacer |
|---|---|---|
| 400 | `INVALID_JSON` · `INVALID_REQUEST` | Corregir el cuerpo: solo `patientId`, `doctorId`, `startAt` (RFC 3339). |
| 400 | `INVALID_IDEMPOTENCY_KEY` | Enviar una clave válida. |
| 400 | `INVALID_PERIOD` | Usar `period=AAAA-MM`. |
| 401 | `UNAUTHORIZED` | Revisar la clave. |
| 403 | `PATIENT_NOT_ENABLED` | El paciente existe pero recepción todavía no lo habilitó. |
| 404 | `UNKNOWN_DOCTOR` · `UNKNOWN_PATIENT` | ID inexistente en la clínica. |
| 404 | `NOT_FOUND` | Turno inexistente o de otro consumidor. |
| 409 | `SLOT_UNAVAILABLE` | Horario ocupado (médico o paciente). Elegir otro. |
| 409 | `IDEMPOTENCY_CONFLICT` | La clave ya se usó con otros datos. |
| 409 | `ALREADY_CANCELLED` | El turno ya estaba cancelado. |
| 415 | `UNSUPPORTED_MEDIA_TYPE` | Enviar `Content-Type: application/json`. |
| 422 | `LEAD_TIME_TOO_SHORT` | Faltan menos de 24 h para el turno (reservar o cancelar). |
| 429 | `RATE_LIMITED` | Esperar `Retry-After`. |
| 503 | `UNAVAILABLE` | Resultado no confirmado. Repetir con la **misma** `Idempotency-Key`, o consultar el turno. |
| 502 · 504 | `UPSTREAM_UNAVAILABLE` · `UPSTREAM_TIMEOUT` | Gateway sin respuesta del servicio. Tratar igual que 503. |

## Migración para Car Cruds
Su adaptador (`services/employees/adapters/clinic`, commit `9eec277`) se construyó leyendo la ruta interna `/api/turnos`. La capacidad publicada mantiene **los mismos campos de respuesta, el mismo 201 y los mismos códigos de rechazo** que ya interpretan. Cambia solo esto:

| Antes (ruta interna, observada) | Ahora (capacidad publicada) |
|---|---|
| `CLINIC_BASE_URL=…/api/turnos` | `CLINIC_BASE_URL=…/v1` (se siguen agregando `/appointments` y `/appointments/{id}`) |
| Sin autenticación | Header `X-API-Key` |
| Sin idempotencia: "enviar como máximo una vez" y conciliar a mano | Header `Idempotency-Key` (sugerido: su `requestId` o `localReference`). Un `unknown` se resuelve repitiendo el POST. |
| Veían cualquier turno | Solo los propios; un turno ajeno da `404` |
| — | Nuevos: `GET /v1/doctors`, `POST /v1/appointments/{id}/cancel`, `GET /v1/usage` |

**Pacientes disponibles hoy** (datos semilla, hasta que el servicio Pacientes esté operativo): `pat-001` y `pat-003` habilitados, `pat-002` registrado sin habilitar. El alta de empleados reales como pacientes se coordina con la clínica.

## Ejecutar y probar en local
```powershell
$env:TURNOS_PARTNER_KEYS = 'carcruds:solo-demo-local-1,otro-grupo:solo-demo-local-2'
docker compose up -d --build
$env:TURNOS_KEY = 'solo-demo-local-1'; $env:TURNOS_KEY_OTHER = 'solo-demo-local-2'
python scripts/check_turnos_contract.py
```
El script valida contra el gateway (`TURNOS_BASE_URL`, por defecto `http://127.0.0.1:8080`) estados, headers y cuerpos de todas las operaciones con el OpenAPI, incluidos idempotencia, aislamiento entre consumidores y facturación. Cada corrida crea y cancela un turno, que queda facturado. Las claves de arriba son solo de ejemplo local.

Configuración del proveedor (servicio Turnos):

| Variable | Default | Uso |
|---|---|---|
| `TURNOS_PARTNER_KEYS` | vacío | `consumidor:clave` separados por coma. Vacío: todas las solicitudes reciben 401. |
| `TURNOS_PARTNER_PRICE_CENTS` | `2500000` | Precio por reserva, en centavos. |
| `TURNOS_PARTNER_CURRENCY` | `ARS` | Moneda ISO 4217. |
| `TURNOS_PARTNER_RATE` | `60` | Solicitudes por minuto por consumidor. |

Un valor inválido de precio, moneda o límite impide iniciar Turnos.

Ejemplos procesables en [examples/turnos](examples/turnos).

## Versionado
`/v1` es la versión mayor. Agregar campos opcionales u operaciones nuevas mantiene `v1` y sube la versión menor: el consumidor debe **tolerar campos de respuesta desconocidos**. Quitar o renombrar campos, agregar campos obligatorios en la solicitud o cambiar el significado de un código exige `/v2` y una guía de migración.

| Versión | Cambio |
|---|---|
| 1.0.0 | Versión inicial (23/10/2026): reservas con clave por consumidor, idempotencia, aislamiento y facturación. |

## Límites conocidos
- Médicos y pacientes provienen de datos semilla; no hay alta de pacientes desde esta API.
- No hay reprogramación: cancelar y reservar de nuevo, con otra clave (se factura de nuevo).
- No hay consulta de disponibilidad: un horario ocupado se informa con `409 SLOT_UNAVAILABLE`.
- El límite de solicitudes es por instancia de Turnos.
- La API no expone información clínica: un turno confirmado **no** acredita asistencia ni aptitud médica.

La API anterior de correos se describe en [NOTIFICATIONS.md](NOTIFICATIONS.md); ya no es la capacidad publicada.
