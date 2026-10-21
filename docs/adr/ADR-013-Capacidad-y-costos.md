# ADR-013 — Capacidad y costos
**Decisión D13 · Estado: versión inicial con método, hipótesis y estimación. Los resultados medidos se incorporan en la presentación final.** No se informan cifras de capacidad que no hayan sido medidas.

## Método
Pruebas de carga con **k6** contra el gateway, con el stack completo de `docker compose` y Grafana como fuente de evidencia.

| Escenario | Carga | Qué valida |
|---|---|---|
| Pico de reservas | Rampa de 0 a 50 usuarios virtuales en 2 min, 60 % sobre el mismo médico | Contención (`booking_lock_wait_seconds`), 409 correctos, p95 de reserva |
| Lectura de disponibilidad | 200 solicitudes/s durante 3 min, con y sin caché | Impacto de D7 (antes/después) |
| API pública de Notificaciones | 60 solicitudes/min por consumidor, con replays | Rate limit, idempotencia y acumulación de correos |
| Caída durante carga | Pico de reservas con RabbitMQ detenido 2 min | D10 y `POSTMORTEM.md` |

Resultados a documentar por escenario: solicitudes/s sostenidas, p50/p95/p99, tasa de errores por código, uso de CPU y memoria por contenedor, y el primer recurso que se satura.

## Hipótesis del principal límite
El cuello esperado **no es la CPU de Turnos**: es la serialización por médico en MySQL. Todas las reservas del mismo médico esperan el mismo bloqueo de recurso, y el pool de 20 conexiones por instancia se llena con transacciones en espera. Se confirma si, al subir la carga, `booking_lock_wait_seconds` p95 crece mientras la CPU de Turnos se mantiene baja.

## Alternativas de escalado
1. **Réplicas de Turnos (D12):** ayudan para médicos distintos, no para el mismo médico, porque el bloqueo es por recurso.
2. **Caché de disponibilidad (D7):** saca las lecturas del camino de MySQL y deja las conexiones a las reservas.
3. **Acortar la transacción:** validar actores antes de abrir la transacción (ya implementado) y mantener solo lectura y escritura bajo bloqueo.
4. **Cola de reservas por médico:** solo si las anteriores no alcanzan; cambia la experiencia a asíncrona.

## Estimación de costos inicial (a validar)
Despliegue mínimo de la capacidad pública (Notificaciones + MySQL + gateway) para la presentación:
- Una VM pequeña (2 vCPU, 4 GB) con Docker Compose, o el plan gratuito o de estudiantes de un proveedor de contenedores.
- MySQL en la misma VM para el TP; en producción, una base gestionada pasaría a ser el principal costo fijo.
- Correo: plan gratuito de un proveedor transaccional; el volumen del TP es bajo.

Los montos se confirmarán con las tarifas vigentes del proveedor elegido al desplegar.
