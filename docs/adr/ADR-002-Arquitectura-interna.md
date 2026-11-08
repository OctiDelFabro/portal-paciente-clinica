# ADR-002 — Arquitectura interna de los servicios
**Decisión D2 · Estado: aceptada e implementada en la Entrega 2.** Turnos usa arquitectura hexagonal; Notificaciones usa arquitectura en capas.

## Contexto
El enunciado exige al menos dos servicios con patrones internos distintos. Los dos servicios operativos de la Entrega 2 tienen problemas diferentes:
- **Turnos** concentra las reglas más delicadas del dominio: anticipación de 24 h, ausencia de superposiciones y concurrencia (RF-02, RF-03). Va a tener más adaptadores con el tiempo: MySQL, RabbitMQ, Pacientes y Médicos por HTTP, Redis (D7).
- **Notificaciones** es mayormente un flujo de entrada → validación → persistencia → envío, con pocas reglas y un contrato público estable.

## Decisión
| Servicio | Patrón | Estructura en el repositorio |
|---|---|---|
| Turnos | Hexagonal (puertos y adaptadores) | `domain/` reglas puras sin dependencias · `app/` casos de uso y puertos (`Store`, `Tx`, `Directory`) · `adapters/` httpapi, mysqlstore, outbox, directory |
| Notificaciones | En capas | `api/` presentación (HTTP y consumidor AMQP) → `service/` negocio → `repository/` datos y `mailer/` integración SMTP |

**Regla de dependencia de Turnos:** `domain` no importa nada del proyecto; `app` importa solo `domain`; los adaptadores implementan los puertos de `app`. Las métricas y el SQL viven en adaptadores, nunca en el dominio.

**Regla de Notificaciones:** cada capa llama solo a la inferior. `service` usa directamente el `repository` concreto; las interfaces existen únicamente donde hay una integración externa que reemplazar o simular (`Mailer`, `Contacts`).

## Justificación
- En Turnos, el dominio puro permite testear las reglas de 24 h y de solapamiento sin base ni red (`domain/appointment_test.go`). Los puertos permiten probar la concurrencia de los casos de uso con un almacenamiento en memoria (`app/service_test.go`) y después con MySQL real (`mysqlstore/store_integration_test.go`).
- Los datos de Pacientes y Médicos todavía vienen de un adaptador semilla. Pasar al cliente HTTP real será agregar un adaptador, sin tocar el dominio.
- En Notificaciones, las capas reducen la ceremonia: no hay reglas que aislar de la tecnología, y el contrato HTTP define el comportamiento. Un hexágono sumaría interfaces sin beneficio.

## Alternativas consideradas
- **Hexagonal en ambos servicios:** sería consistente, pero no cumple el requisito de patrones distintos y en Notificaciones no aporta.
- **Capas en Turnos:** acoplaría las reglas de reserva al SQL. La regla de solapamiento quedaría repartida entre el servicio y las consultas.
- **CQRS en Turnos:** se evalúa para disponibilidad junto con la caché (D7). Hoy no hay un modelo de lectura que lo justifique.

## Consecuencias
- Turnos tiene más archivos y una indirección extra (puertos). Se acepta a cambio de poder testear y reemplazar los adaptadores.
- En Notificaciones, cambiar de MySQL implicaría modificar `service`. Se acepta porque el almacenamiento no es un punto de variación previsto.
- El gateway es un componente de infraestructura sin dominio; no aplica ninguno de los dos patrones.

## Referencias
- Turnos: `services/turnos/internal/{domain,app,adapters}`.
- Notificaciones: `services/notifications/internal/{api,service,repository,mailer}`.
- [Alistair Cockburn — Hexagonal architecture](https://alistair.cockburn.us/hexagonal-architecture/).
