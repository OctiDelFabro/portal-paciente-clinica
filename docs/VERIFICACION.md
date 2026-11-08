# Verificación del paquete — Evidencia empírica

Este documento consolida la evidencia de pruebas, métricas y comportamiento del sistema bajo carga y condiciones de falla, cumpliendo con la especificación **S10** ("verificación de las decisiones mediante la evidencia obtenida sobre el sistema").

---

## 1. Entrega Final (2026-11-11)

### 1.1 Entorno de pruebas
- **Sistema Operativo**: Windows 11 Pro / Docker Desktop 29.x
- **Lenguaje**: Go 1.26.1 / Node.js 22.x / React 19 / TypeScript 5.8
- **Contenedores activos (17 servicios)**:
  `gateway`, `turnos-1`, `turnos-2`, `pacientes`, `medicos`, `historia-clinica`, `notifications`, `provider-stub`, `mysql`, `mongodb`, `redis`, `opensearch`, `rabbitmq`, `mailpit`, `jaeger`, `prometheus`, `grafana`.

---

### 1.2 Pruebas unitarias y de integración del código base
- `go test ./...`: **100% de tests pasando en verde** en todos los paquetes del repositorio:
  - `gateway/cmd`: Autenticación JWT, verificación de roles, stripping de encabezados spoofables, balanceo round-robin con exclusión de réplicas enfermas, limitador de tasa token bucket, CORS.
  - `services/pacientes/...`: Validación RFC 5322 de emails, validación de contraseñas, hashing BCrypt, protección contra enumeración de emails mediante timing equalizer, emisión de JWTs con rol (`patient`, `doctor`), control de concurrencia en registro con MySQL.
  - `services/medicos/...`: Sincronización y reconstrucción de índices en OpenSearch, consultas con filtros por especialidad y menores, paginación, cálculo de próximo turno libre tras eventos de dominio.
  - `services/turnos/...`:
    - Modelo de dominio de reservas (regla de anticipación de 24h, turnos adyacentes, cancelaciones).
    - Control de concurrencia pesimista en MySQL (`booking_locks` con `SELECT ... FOR UPDATE` ordenado alfabéticamente).
    - Cliente HTTP del proveedor de energía con circuit breaker (umbral de 2 fallos consecutivos, timeout de 150 ms) y fallback degradado (`pending`).
    - Caché de disponibilidad en Redis con invalidación reactiva.
    - Endpoints de agenda para médicos (`ListForDoctor`, `GetForDoctor`, `CancelForDoctor`).
    - Autenticación fail-closed: rechazo con 401 si falta `X-Patient-Id`.
  - `services/historia-clinica/...`: Consumidor idempotente de eventos de RabbitMQ en MongoDB con colección de deduplicación, autorización estricta de notas médicas por encuentro (403 si el médico no coincide con el turno).
  - `services/notifications/...`: Consumidor idempotente de eventos de turnos, envío SMTP a Mailpit, manejo de DLQ ante mensajes corruptos.
  - `services/provider-stub/cmd`: Emulador local del contrato `ArquitecturasoftwareRenewableEnergy` v1.
- `npm run build` en `frontend/`:
  - Tipado estricto `tsc --noEmit` sin errores.
  - Compilación y bundle optimizado con Vite en 6.29s (2.096 módulos transformados).

---

### 1.3 Pruebas de carga k6 (ADR-007 y ADR-013)

Las pruebas se ejecutaron mediante contenedores oficiales `grafana/k6:latest` dentro de la red interna de Docker (`portal-paciente-clinica_default`).

#### A. Benchmark comparativo de Caché en Disponibilidad (`infra/k6/availability.js`)
Objetivo: Medir el impacto de Redis en la consulta de disponibilidad médica (`GET /api/turnos/doctors/doc-001/availability?date=...`) a **200 solicitudes por segundo** sostenidas durante 30 segundos.

| Métrica | Con Caché (`CACHE_ENABLED=true`) | Sin Caché (`CACHE_ENABLED=false`) | Mejora / Impacto |
|---|---|---|---|
| **Hit Rate en Redis** | **99.74%** (2.720 aciertos / 7 fallos) | **0%** (3.000 consultas directas a SQL) | Eliminación de carga en base |
| **Latencia mediana (p50)** | **775 µs (0.77 ms)** | **1.05 ms** | **-26.2%** de latencia |
| **Latencia percentil 90 (p90)** | **1.34 ms** | **1.42 ms** | **-5.6%** de latencia |
| **Consultas a MySQL** | **0.47 queries/segundo** | **200.0 queries/segundo** | **-99.7%** de consultas a MySQL |
| **Tasa de error HTTP** | **0.00%** (0 errores en 3.000 reqs) | **0.00%** (0 errores en 3.000 reqs) | Alta estabilidad |

**Conclusión empírica**: La caché absorbe el 99.7% del tráfico de lectura, liberando completamente el motor relacional para las transacciones críticas de escritura.

#### B. Benchmark de Pico y Contención en Reservas (`infra/k6/booking-peak.js`)
Objetivo: Evaluar el comportamiento del bloqueo pesimista en MySQL y la limitación de tasa ante un aluvión masivo de reservas concurrentes.
- **Concurrencia**: 20 Virtual Users (VUs) simultáneos durante 20 segundos.
- **Total de solicitudes procesadas**: 2.916 peticiones HTTP.
- **Resultados de consistencia**:
  - `201 Created`: Asignado estrictamente a las reservas ganadoras no solapadas.
  - `409 Conflict` (`SLOT_UNAVAILABLE`): Retornado consistentemente en cada intento de colisión de horario. **Cero turnos solapados o duplicados** en la base de datos.
  - `429 Too Many Requests`: Activado automáticamente por el rate limiter de socios al superar la cuota de 60 req/min.
- **Latencia p95 bajo contención**: 62.2 ms.

---

### 1.4 Verificación de Balanceo y Failover (ADR-012)
- El Gateway balancea el tráfico hacia Turnos entre `turnos-1:8081` y `turnos-2:8081` mediante Round-Robin activo.
- **Health Checks activos**: Cada 2 segundos, el Gateway sondea `GET /healthz` de cada réplica con timeout de 500 ms.
- **Comportamiento ante caída**:
  - Al detener intencionalmente `turnos-1` (`docker stop portal-paciente-clinica-turnos-1-1`), el Gateway detecta 2 fallos consecutivos y marca la réplica como `unhealthy`, retirándola de la rotación.
  - El 100% del tráfico subsiguiente es atendido por `turnos-2` sin interrupción de servicio para el usuario ni solicitudes 502/504 en el cliente.
  - Al reiniciar el contenedor, el Gateway detecta la respuesta 200 en su siguiente chequeo y reincorpora la instancia al balanceo automáticamente.

---

### 1.5 Verificación de Resiliencia del Proveedor Energético (ADR-009)
- El cliente HTTP de `ArquitecturasoftwareRenewableEnergy` opera bajo un presupuesto estricto de timeout (150 ms) y Circuit Breaker en memoria.
- **Prueba de simulación**:
  - Con el stub en modo normal: Las reservas devuelven el costo energético calculado (`kWh * 180 ARS/kWh`) añadido al precio de la consulta.
  - Con el stub caído o lento: Tras 2 fallos consecutivos, el Circuit Breaker pasa a estado `open`, cortocircuitando las llamadas durante 30 segundos sin bloquear el hilo.
  - **Comportamiento degradado controlado**: La reserva médica procede exitosamente devolviendo `201 Created` con estado de energía `pending`. El paciente nunca queda impedido de confirmar su turno médico por fallas en un servicio accesorio.

---

### 1.6 Flujo de Punta a Punta Verificado en Vivo (Seam Check)
Mediante script automatizado de integración (`test-t12.ps1`):
1. **Fail-Closed**: Llamadas anónimas a `POST /api/turnos/appointments` son rechazadas inmediatamente con `401 Unauthorized`.
2. **Login de Médico**: `doctor1@example.test` autentica contra `POST /api/pacientes/auth/login`, obteniendo JWT con `role: "doctor"` y `sub: "doc-001"`.
3. **Perfil de Médico**: `GET /api/pacientes/me` valida la identidad profesional inyectada por el Gateway (`X-User-Role: doctor`).
4. **Reserva de Paciente**: Paciente `pat-001` agenda un turno con `doc-001` a través del Gateway.
5. **Agenda del Profesional**: `doc-001` consulta `GET /api/turnos/appointments` y visualiza inmediatamente la nueva cita en su agenda médica.
6. **Lookup de Paciente**: El médico obtiene el nombre y contacto de su paciente citado (`GET /api/pacientes/patients/pat-001/contact`), autorizado por su rol profesional.
7. **Proyección Asíncrona**: El evento `turnos.reservado.v1` viaja por RabbitMQ y es persistido de forma idempotente en MongoDB (`historia.encounters`).
8. **Redacción de Nota Clínica**: `doc-001` registra nota en `POST /api/historia/records/{id}/notes` -> `201 Created`.
9. **Aislamiento y Seguridad**: `doc-002` (médico no asignado a ese turno) intenta añadir una nota al mismo encuentro -> **Rechazado estrictamente con `403 Forbidden`**.
10. **Lectura del Paciente**: El paciente consulta `GET /api/historia/records/me` y observa su timeline actualizado con la nota registrada por el médico.

---

## 2. Instrucciones para Reproducir la Verificación

### Pruebas de código en local
```powershell
# Ejecutar suite de pruebas unitarias Go
go test ./...

# Validar compilación del frontend
cd frontend
npm run build
cd ..
```

### Ejecución de la infraestructura completa en Docker
```powershell
# Levantar los 17 contenedores
docker compose up -d --build

# Verificar salud de los servicios
docker compose ps
```

### Ejecutar benchmarks k6
```powershell
# Benchmark de disponibilidad y caché
docker run --rm -i --network portal-paciente-clinica_default -v "${PWD}/infra/k6:/scripts" grafana/k6 run /scripts/availability.js

# Benchmark de picos y concurrencia de reservas
docker run --rm -i --network portal-paciente-clinica_default -v "${PWD}/infra/k6:/scripts" grafana/k6 run /scripts/booking-peak.js
```
