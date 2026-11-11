# ADR-004 — Control de concurrencia y prevención de solapamientos

**Decisión D4 · Estado: Aprobada e implementada (Entrega 2 y Entrega Final).**

## Contexto

En el sistema de gestión de turnos médicos, múltiples actores (pacientes a través del portal web y sistemas externos/socios a través de la API publicada) intentan reservar, consultar y cancelar citas simultáneamente.

Los principales desafíos de concurrencia identificados son:
1. **Doble reserva (Double Booking)**: Dos pacientes intentan reservar el mismo horario con el mismo profesional al mismo tiempo.
2. **Solapamiento de citas (Overlapping Appointments)**: Intervalos de distinta duración (ej. 30 vs 60 minutos) que se cruzan parcialmente. Una restricción única SQL clásica sobre `(doctor_id, start_at)` es insuficiente para detectar colisiones cuando los turnos tienen duraciones heterogéneas.
3. **Reserva concurrente del mismo paciente**: Un paciente intentando reservar citas superpuestas con distintos médicos.
4. **Interbloqueos (Deadlocks)**: Bloqueos de múltiples recursos en transacciones concurrentes con orden arbitrario que pueden generar interbloqueos en el motor de base de datos.
5. **Condiciones de carrera en cancelación/actualización**: Modificaciones concurrentes sobre el estado de un turno ya cancelado o reprogramado.

## Decisión

Se adopta un modelo híbrido: **bloqueo pesimista ordenado a nivel de recurso para reservas**, combinado con **control optimista de versiones (`version`) para modificaciones/cancelaciones**, complementado por **claves de idempotencia y limitación de tasa**.

### 1. Bloqueo Pesimista Ordenado (`booking_locks`)
Para la creación de reservas (`Book` / `BookForPatient` / `BookForPartner`), se utiliza una tabla de recursos dedicada en MySQL (`booking_locks`):

1. **Orden estable de claves**: Antes de adquirir los bloqueos, las claves del médico (`doctor:<id>`) y del paciente (`patient:<id>`) se ordenan lexicográficamente (alfabético estricto). Esto previene ciclos de espera en el grafo de bloqueos y elimina deadlocks entre transacciones concurrentes:
   ```sql
   SELECT resource_key FROM booking_locks 
   WHERE resource_key IN (?, ?) 
   ORDER BY resource_key FOR UPDATE;
   ```
2. **Verificación de intervalos semiabiertos**: Protegido bajo las filas bloqueadas, se ejecuta la verificación de no-superposición sobre intervalos semiabiertos `[start, end)`:
   ```sql
   SELECT id, doctor_id, patient_id, start_at, end_at, status, version
   FROM appointments
   WHERE status = 'booked' 
     AND (doctor_id = ? OR patient_id = ?) 
     AND start_at < ? AND end_at > ?;
   ```
   Si se encuentra alguna cita activa que solape el rango deseado, se aborta la transacción y se retorna `409 Conflict` con código `SLOT_UNAVAILABLE`.
3. **Inserción atómica**: Si no hay solapamiento, se inserta la nueva cita con `version = 1`, su cálculo de energía asociado y el evento outbox en la misma transacción atómica.

### 2. Control Optimista de Concurrencia para Actualizaciones
Para transiciones de estado de turnos existentes (ej. cancelación):
- La tabla `appointments` incluye una columna `version INT NOT NULL`.
- Las actualizaciones aplican control optimista comprobando la versión previa:
  ```sql
  UPDATE appointments 
  SET status = ?, version = ?, cancel_reason = NULLIF(?, '')
  WHERE id = ? AND version = ?;
  ```
- Si `RowsAffected == 0`, significa que el turno fue modificado concurrentemente (ej. ya cancelado), rechazándose con `409 Conflict` (`ALREADY_CANCELLED` o conflicto concurrente).

### 3. Nivel de Aislamiento
Se configura el aislamiento transaccional en `READ COMMITTED` (ADR-003). La serialización rigurosa de las reservas la garantizan los bloqueos explícitos de `booking_locks`, evitando los *gap locks* excesivos que produce `REPEATABLE READ` en MySQL/InnoDB.

### 4. Idempotencia y Rate Limiting
- **Socios externos**: Las solicitudes `POST /v1/appointments` requieren encabezado `Idempotency-Key`. La tabla `partner_bookings` registra `(consumer, idempotency_key)` con restricción `UNIQUE`. Reintentos con la misma clave retornan el turno original (`201 Created` con `Idempotency-Replayed: true`) sin recrearlo; cargas con misma clave pero distinto contenido devuelven `409 Conflict` (`IDEMPOTENCY_CONFLICT`).
- **Límite de tasa**: Token bucket en memoria (`apikey.Limiter`) restringe el tráfico de socios a un máximo configurable (ej. 60 req/min), respondiendo `429 Too Many Requests` ante ráfagas excesivas.

## Alternativas consideradas

- **Restricción UNIQUE sobre (doctor_id, start_at)**: Descartada porque falla cuando hay citas de diferente duración que solapan un horario (ej. una cita de 9:00 a 10:00 solapa una de 9:30 a 10:00).
- **Aislamiento Serializable en MySQL**: Descartada por alto impacto de contención, penalización de throughput y frecuentes abortos por deadlock bajo concurrencia moderada.
- **Bloqueos distribuidos en Redis (Redlock)**: Descartada para la consistencia transaccional primaria; introduce complejidad operativa y posible pérdida de garantías ante particiones de red o reinicios de Redis. Redis se mantiene estrictamente como caché de lectura (ADR-007).

## Evidencia y Verificación

1. **Pruebas de integración automatizadas (`store_integration_test.go`)**:
   - 15 reservas concurrentes sobre el mismo profesional: exactamente 1 gana con `201 Created` y 14 son rechazadas con `409 Conflict` (`SLOT_UNAVAILABLE`). Cero dobles reservas.
2. **Benchmark de Carga k6 (`infra/k6/booking-peak.js`)**:
   - 20 VUs concurrentes compitiendo durante 20 segundos generando 2.916 solicitudes bajo contención.
   - 100% de consistencia: las colisiones de horario retornaron consistentemente `409 Conflict`, los excesos de cuota retornaron `429 Too Many Requests`, y los ganadores obtuvieron `201 Created`.
   - Latencia p95 reportada: 62.2 ms (antecedente sin reejecución validada; ver `docs/VERIFICACION.md`).

## Consecuencias

- **Positivas**:
  - Garantía matemática contra solapamientos y dobles reservas.
  - Ausencia de interbloqueos cíclicos gracias al ordenamiento lexicográfico de claves.
  - Resiliencia comprobada bajo picos de alta contención.
- **Trade-offs**:
  - Las reservas para el mismo médico se serializan en la base de datos (throughput acotado por médico individual, pero completamente paralelizado entre médicos distintos).
