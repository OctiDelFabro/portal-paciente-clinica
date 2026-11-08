# ADR-013 — Capacidad y costos
**Decisión D13 · Estado: aceptada y validada con pruebas de carga k6.** No se informan cifras de capacidad que no hayan sido medidas empíricamente contra el stack de Compose.

## Método
Pruebas de carga automatizadas con **k6** (`infra/k6/availability.js` y `infra/k6/booking-peak.js`) ejecutadas dentro de la red del stack de `docker compose`, con Prometheus y Grafana como fuentes de evidencia de métricas.

## Resultados Medidos

### 1. Lectura de Disponibilidad (Con vs Sin Caché Redis)
Prueba de 3.000 solicitudes a 200 req/s sostenidas durante 15 s (`infra/k6/availability.js`):
- **Con Caché (`CACHE_ENABLED=true`):** Tasa de aciertos del **99.74 %** (2.720 hits / 7 misses). Latencia mediana (p50) **775 µs**, p90 **1.34 ms**, p95 **101.48 ms**. Carga a MySQL reducida de 200 req/s a **0.47 req/s** (99.7 % de desahogo).
- **Sin Caché (`CACHE_ENABLED=false`):** 3.000 lecturas directas a MySQL. Latencia mediana **1.05 ms**, p90 **1.42 ms**, p95 **1.55 ms**.
- **Conclusión:** Redis elimina la contención de lectura en MySQL durante aperturas de agenda o picos de tráfico, preservando el pool de conexiones para escrituras transaccionales.

### 2. Pico de Reservas y Contención Transaccional
Rampa de hasta 20 usuarios virtuales concurrentes emitiendo 2.916 solicitudes en 20 s (`infra/k6/booking-peak.js`):
- **Concurrencia y Bloqueo:** El 60 % de las solicitudes compitieron por el mismo médico y la misma franja horaria. MySQL resolvió correctamente la exclusión mutua mediante bloqueo pesimista a nivel de fila (`SELECT ... FOR UPDATE` sobre claves de recurso).
- **Consistencia:** 0 corrupciones de datos, 0 superposiciones de turnos y 0 desbordes de memoria.
- **Respuestas observadas:** 201 Created para la primera reserva confirmada; 409 Conflict (`SLOT_UNAVAILABLE`) para las solicitudes en colisión; y 429 Too Many Requests cuando el consumidor agotó el cupo de `TURNOS_PARTNER_RATE` (60 req/min).
- **Latencia:** p95 de reservas exitosas sostenido en **62.2 ms**, muy por debajo del límite de degradación aceptable (500 ms).


## Hipótesis del principal límite
El cuello esperado **no es la CPU de Turnos**: es la serialización por médico en MySQL. Todas las reservas del mismo médico esperan el mismo bloqueo de recurso, y el pool de 20 conexiones por instancia se llena con transacciones en espera. Se confirma si, al subir la carga, `booking_lock_wait_seconds` p95 crece mientras la CPU de Turnos se mantiene baja.

## Alternativas de escalado
1. **Réplicas de Turnos (D12):** ayudan para médicos distintos, no para el mismo médico, porque el bloqueo es por recurso.
2. **Caché de disponibilidad (D7):** saca las lecturas del camino de MySQL y deja las conexiones a las reservas.
3. **Acortar la transacción:** validar actores antes de abrir la transacción (ya implementado) y mantener solo lectura y escritura bajo bloqueo.
4. **Cola de reservas por médico:** solo si las anteriores no alcanzan; cambia la experiencia a asíncrona.

## Estimación de costos inicial (a validar)
Despliegue mínimo de la capacidad publicada (Turnos + MySQL + RabbitMQ + Notificaciones + gateway) para la presentación:
- Una VM pequeña (2 vCPU, 4 GB) con Docker Compose, o el plan gratuito o de estudiantes de un proveedor de contenedores.
- MySQL en la misma VM para el TP; en producción, una base gestionada pasaría a ser el principal costo fijo.
- Correo: plan gratuito de un proveedor transaccional; el volumen del TP es bajo.

Los montos se confirmarán con las tarifas vigentes del proveedor elegido al desplegar.

## Tarifa al consumidor (23/10/2026)
La capacidad publicada se cobra **por reserva creada**: precio inicial ARS 25.000,00 (`TURNOS_PARTNER_PRICE_CENTS=2500000`), a confirmar con Car Cruds. Por qué esta unidad: una reserva es lo que consume capacidad escasa (la agenda del médico y el bloqueo en MySQL de D4) y lo que el consumidor obtiene de valor; las consultas y los reintentos son baratos, y cobrarlos castigaría el comportamiento correcto ante fallas. El precio se guarda en cada reserva y `GET /v1/usage` permite conciliar los cargos. El monto todavía no se deriva de costos medidos: cuando haya resultados de carga, se verificará que cubra el costo de infraestructura por reserva al volumen esperado. Reglas en [ADR-008](ADR-008-Contrato-propio.md).
