# portal-paciente-clinica — Arquitectura
`docs/ARCHITECTURE.md` · Versión Entrega Final (2026-11-11)

Describe la arquitectura del portal de pacientes y profesionales para la Clínica Arroyo Claro, implementado como un sistema distribuido de microservicios con políglota de persistencia, mensajería asíncrona, balanceo de carga, caché distribuida, búsqueda indexada, observabilidad de extremo a extremo e integración con servicios de terceros.

---

## 1. Contexto y alcance

El portal atiende a los pacientes, profesionales médicos y personal de recepción de la clínica:
- **Pacientes**: Registro autogestionado, inicio de sesión seguro, búsqueda de profesionales por especialidad y disponibilidad, cotización transparente con costo energético, reserva de turnos, consulta de historial de citas y visualización de su historia clínica y notas de atención.
- **Médicos**: Inicio de sesión con rol profesional (`doctor`), visualización de su agenda diaria/semanal, acceso a datos de contacto de sus pacientes citados y registro autorizado de notas clínicas en los encuentros de atención.
- **Recepción / Administración**: Verificación de pacientes, gestión de vínculos familiares (menores a cargo) y bloqueos de agenda.
- **Socios externos (Car Cruds)**: Consumo M2M de la capacidad propia de Turnos (`/v1/appointments`, `/v1/doctors`, `/v1/usage`) autenticado mediante clave de API (`X-API-Key`) e idempotencia (`Idempotency-Key`), con facturación en ARS (ADR-008).
- **Proveedor externo (ArquitecturasoftwareRenewableEnergy)**: Consulta síncrona HTTP desde el microservicio de Turnos para estimar el consumo eléctrico (kWh) del equipamiento médico de cada consulta, calculando la clínica el costo en pesos según su tarifa interna (ADR-009, ADR-014).

---

## 2. Componentes y distribución del sistema

```
                                  [ Navegador Web / Cliente ]
                                               │
                                               ▼
                                 ┌───────────────────────────┐
                                 │     Frontend (Vite/React) │
                                 │   SPA TypeScript / CSS    │
                                 └─────────────┬─────────────┘
                                               │ HTTP (Port 8080 / 8088)
                                               ▼
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                              API Gateway (Go net/http)                                 │
 │  - Autenticación JWT (ADR-014)           - Rate Limiting por IP/Token                  │
 │  - Health Checks activos (2s)            - Balanceo Round-Robin Turnos (ADR-012)       │
 └───────┬──────────────┬──────────────┬──────────────┬──────────────┬────────────────────┘
         │              │              │              │              │
         │ :8082        │ :8083        │ :8081        │ :8084        │ :8085
         ▼              ▼              ▼              ▼              ▼
   ┌───────────┐  ┌───────────┐  ┌───────────┐  ┌───────────┐  ┌───────────┐
   │ Pacientes │  │  Médicos  │  │  Turnos   │  │  Historia │  │Notificac. │
   │   (Go)    │  │   (Go)    │  │(Turnos-1/2│  │  Clínica  │  │   (Go)    │
   └─────┬─────┘  └─────┬─────┘  └─────┬─────┘  └─────┬─────┘  └─────┬─────┘
         │              │              │              │              │
         ▼              ▼              ▼              │              ▼
   ┌───────────┐  ┌───────────┐  ┌───────────┐        │        ┌───────────┐
   │   MySQL   │  │   MySQL   │  │   MySQL   │        │        │   MySQL   │
   │ pacientes │  │  medicos  │  │  turnos   │        │        │  notific. │
   └───────────┘  └─────┬─────┘  └─────┬─────┘        │        └───────────┘
                        │              │              │
                        ▼              ├──────────────┤
                  ┌───────────┐        │  RabbitMQ    │◄─ (Outbox publisher)
                  │OpenSearch │        │  Events      │
                  │  Index    │        └──┬────────┬──┘
                  └───────────┘           │        │
                                          ▼        ▼
                                   ┌──────────┐ ┌──────────┐
                                   │ Historia │ │Notificac.│
                                   │ Consumer │ │ Consumer │
                                   └────┬─────┘ └────┬─────┘
                                        ▼            ▼
                                  ┌──────────┐ ┌──────────┐
                                  │ MongoDB  │ │ Mailpit  │
                                  │ Encounters│ │  SMTP    │
                                  └──────────┘ └──────────┘
```

### 2.1 Frontend Web — React 19, Vite, TypeScript
- Aplicación de página única (SPA) sin librerías pesadas de interfaz, desarrollada bajo un diseño visual moderno (*Boarding Pass / Live Board*) centrado en accesibilidad y claridad operativa.
- Flujos completos: Registro y login con autodetección de rol (`patient` o `doctor`), buscador reactivo de profesionales con filtros de especialidad y ordenamiento, confirmación de reserva con desglose de costo energético, timeline clínico personal, y pantalla de agenda profesional para médicos con formulario de notas de atención.

### 2.2 API Gateway — Go `net/http`
- Punto único de entrada al clúster (ADR-008, ADR-012, ADR-014).
- Expone dos escuchas independientes:
  - **Puerto Portal (`8080`)**: Atiende `/api/*` y `/metrics`. Valida tokens JWT emitidos por Pacientes, limpia encabezados spoofables del cliente (`X-Patient-Id`, `X-User-Role`), inyecta la identidad verificada y aplica limitación de tasa por IP o sujeto.
  - **Puerto Público (`8088`)**: Atiende `/v1/*` (capacidad propia para Car Cruds) y `/healthz`. Aislamiento estricto: no rutea rutas del portal ni métricas internas.
- **Balanceo dinámico**: Mantiene un pool de réplicas para el servicio de Turnos (`turnos-1` y `turnos-2`). Ejecuta verificaciones de salud activas cada 2 segundos (`GET /healthz`), retirando de la rotación instancias caídas tras 2 fallos consecutivos y reincorporándolas automáticamente.

### 2.3 Turnos — Go (Arquitectura Hexagonal) · Operativo Replicado
- Autoridad única sobre la agenda, disponibilidad, duraciones de cita, precios y reservas.
- **Control de concurrencia (ADR-004)**: Bloqueo pesimista a nivel de fila (`booking_locks`) ordenado lexicográficamente por médico y paciente (`SELECT ... FOR UPDATE`), garantizando cero deadlocks y prevención absoluta de citas solapadas sobre intervalos semiabiertos `[start, end)`.
- **Caché distribuida (ADR-007)**: Integrada en la consulta de disponibilidad (`/doctors/{id}/availability?date=...`) usando Redis con TTL de 5 minutos e invalidación reactiva inmediata tras cada reserva o cancelación. Demostró una reducción del 99.7% en consultas SQL y latencias p50 de 775 µs bajo 200 req/s.
- **Consumo de Energía (ADR-009, S14)**: Invoca al proveedor externo `ArquitecturasoftwareRenewableEnergy` (`POST /api/v1/estimaciones-consumo`) con circuit breaker (umbral de 2 fallos consecutivos, timeout de 150 ms, fallback degradado en estado `pending`).
- **Publicación Transaccional (Outbox, ADR-005)**: Escribe eventos de dominio en la tabla `outbox` en la misma transacción MySQL que el turno, con un despachador en segundo plano que publica hacia RabbitMQ (`turnos.exchange`).

### 2.4 Pacientes — Go (Arquitectura en Capas)
- Almacena perfiles de pacientes, vínculos entre adultos y menores (`responsible_id`), estado de habilitación y cuentas de acceso.
- Criptografía segura: Contraseñas hasheadas con BCrypt (costo 10 en producción, mínimo en pruebas).
- Autenticación y Emisión de Identidad (ADR-014): Emite JWTs firmados con HS256 (`JWT_SECRET` compartido con el Gateway) con claims estandarizados (`sub`, `role`, `iss`, `iat`, `exp`). Soporta roles `patient` y `doctor`.
- Sembrado automático de cuentas demo operativas (`paciente1@example.test`, `doctor1@example.test`) con contraseñas seguras configurables.

### 2.5 Médicos — Go (Arquitectura en Capas)
- Administra el maestro de profesionales y especialidades médicas en MySQL.
- **Búsqueda indexada en OpenSearch (ADR-006, S9)**: Sincroniza perfiles hacia el índice `doctors` en OpenSearch. Provee búsqueda de texto completo difusa (`match` / `prefix`), filtrado exacto por código de especialidad, soporte para menores (`acceptsMinors`), ordenamiento por nombre o próxima disponibilidad (`nextAvailableAt`), y paginación estructurada.
- Consumidor RabbitMQ: Escucha eventos `turnos.reservado.v1` y `turnos.cancelado.v1` para recalcular y reindexar asíncronamente en OpenSearch la fecha del próximo turno libre de cada profesional.

### 2.6 Historia Clínica — Go (Arquitectura en Capas) · MongoDB
- Base de datos no relacional orientada a documentos (MongoDB, ADR-003). Almacena documentos de atención médica enriquecibles con esquemas JSON Schema flexibles.
- **Consumidor Idempotente**: Escucha eventos de Turnos desde RabbitMQ y actualiza la colección `encounters` usando colección de deduplicación `processed_events`.
- **Autorización por encuentro (T12)**: El endpoint `POST /records/{appointmentId}/notes` exige rol `doctor` y verifica en la base de datos que el médico que escribe sea exactamente el profesional asignado a esa consulta (retornando `403 Forbidden` si otro médico intenta redactar notas).

### 2.7 Notificaciones — Go (Arquitectura en Capas)
- Gestión de plantillas y catálogo de avisos.
- Consumidor de eventos de reserva y cancelación con despacho de correos electrónicos transaccionales a través de Mailpit SMTP e inbox idempotente contra duplicados.
- Capacidad de simulación y testing local.

---

## 3. Propiedad y segregación de datos

| Servicio | Motor de Base de Datos | Base / Colección | Rol del Almacenamiento |
|---|---|---|---|
| Turnos | MySQL 8.4 (InnoDB) | `turnos` | Transaccional primario (citas, bloqueos, outbox, facturación socios) |
| Turnos (Caché) | Redis 7.2 | DB 0 (`availability:v1:*`) | Proyección volátil de disponibilidad de lectura (TTL 5 min) |
| Pacientes | MySQL 8.4 (InnoDB) | `pacientes` | Transaccional primario (usuarios, roles, hashes, vínculos familiares) |
| Médicos | MySQL 8.4 (InnoDB) | `medicos` | Maestro autoritativo relacional (médicos, especialidades) |
| Médicos (Búsqueda) | OpenSearch 2.18 | Índice `doctors` | Proyección optimizada para búsqueda full-text y filtros |
| Historia Clínica | MongoDB 7.0 | `historia.encounters` | Documental (atenciones, diagnósticos, notas clínicas) |
| Historia Clínica (Inbox) | MongoDB 7.0 | `historia.processed_events` | Control de idempotencia de mensajería |
| Notificaciones | MySQL 8.4 (InnoDB) | `notificaciones` | Registro de envíos, plantillas, avisos y claves de idempotencia |

Ningún servicio accede de forma directa a la base de datos de otro. Todo cruce de información se efectúa mediante contratos HTTP tipados o eventos de dominio asíncronos.

---

## 4. Limitaciones conocidas y deuda técnica aceptada (S11)

En cumplimiento con la especificación S11 de la Entrega Final, se explicitan los compromisos de diseño y la deuda técnica asumida:

| Ítem | Descripción y Motivo | Deuda técnica aceptada | Estrategia de mitigación / Futuro |
|---|---|---|---|
| **Revocación de JWT** | La autenticación del portal utiliza JWTs firmados con HS256 sin verificación contra base de datos en cada petición del Gateway (ADR-014). | Si un usuario es inhabilitado por recepción o se cambia su rol, su token emitido permanece válido hasta su expiración (TTL de 60 minutos). | Aceptada para mantener el Gateway liviano y desacoplado de MySQL. Mitigación: TTL corto (1h); a futuro, lista negra de tokens revocados en Redis con tiempo de expiración sincronizado. |
| **Rate Limiter en Memoria en Gateway** | El control de tasa de solicitudes del portal y de socios se computa con un algoritmo Token Bucket en la memoria del proceso del Gateway. | Si se despliegan múltiples réplicas del Gateway detrás de un Load Balancer L4/L7, el límite efectivo se multiplica por la cantidad de réplicas. | Aceptada para el alcance actual de una única instancia de Gateway. En un entorno multi-gateway, migrar los cubos a Redis mediante scripts Lua atómicos. |
| **Algoritmo de Balanceo Round-Robin Simple** | El Gateway distribuye equitativamente las peticiones entre `turnos-1` y `turnos-2` (ADR-012) mediante un contador atómico circular. | No pondera la latencia instantánea, uso de CPU o memoria de cada réplica de backend. | Aceptada: las consultas de Turnos tienen tiempos de procesamiento homogéneos y la concurrencia está acotada. Se mitiga con verificaciones de salud activas cada 2s que aíslan nodos con fallas. |
| **Consumo del Proveedor Energético Degradado** | El microservicio de Turnos consulta la estimación de consumo energético mediante un circuit breaker local con timeout estricto de 150 ms (ADR-009). | Ante caída o lentitud del proveedor externo, el turno se confirma con estado de energía `pending` y costo cero en la reserva inmediata. | Decisión consciente de disponibilidad sobre consistencia inmediata: una falla en un servicio accesorio de estimación no debe impedir la atención médica del paciente. Un worker de reconciliación reintenta la cotización en segundo plano. |
| **Tarifa Energética en ARS Local** | El proveedor externo `ArquitecturasoftwareRenewableEnergy` v1 únicamente devuelve estimación física en kWh; no cotiza valores monetarios. | El cálculo monetario en ARS se efectúa con una constante de tarifa propia configurada en Turnos ($180 ARS/kWh). | Aceptada: refleja fielmente la realidad del negocio, donde la clínica fija su propio precio de venta por la energía consumida en el consultorio. |
| **Filtro de contingencia en Búsqueda de Médicos** | La búsqueda de profesionales prioriza OpenSearch. | Si OpenSearch no estuviera disponible, no existe un fallback SQL complejo que emule la búsqueda semántica difusa y fonética. | Aceptada: OpenSearch cuenta con reinicio automático de contenedor en Docker Compose y volumen persistente de datos. |
| **Despacho de Correos a Mailpit** | Las notificaciones por email se envían a través del servidor local Mailpit emulando SMTP. | No se envían correos a bandejas reales de Internet para evitar costos de infraestructura externa (SendGrid/AWS SES) y rebotes de prueba. | Aceptada: Mailpit expone interfaz gráfica web y API REST para auditar el contenido exacto y destinatarios de cada mensaje saliente. |
