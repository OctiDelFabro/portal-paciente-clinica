# ADR-009 — Consumo de la capacidad del proveedor
**Decisión D9 · Estado: criterios aceptados; proveedor pendiente de asignación por la cátedra.** Las reglas de integración ya están definidas, así que adaptarlas al contrato concreto será acotado cuando se asigne el proveedor.

## Contexto
El portal debe consumir una capacidad publicada por otro grupo e incorporarla a un flujo importante. La llamada debe salir del microservicio responsable de esa funcionalidad, no del frontend ni del gateway. Al cierre de la Entrega 2 la cátedra todavía no asignó el proveedor; no se inventa una integración.

## Decisión
1. **Ubicación:** el servicio dueño del flujo afectado (por ejemplo, Turnos si la capacidad complementa la reserva) tiene un adaptador de salida propio. En Turnos es un puerto nuevo en `app/`, implementado en `adapters/`, siguiendo D2.
2. **Capa anticorrupción:** el adaptador traduce el modelo del proveedor al nuestro. Ningún tipo ni código de error del proveedor sale del adaptador.
3. **Contrato:** se trabaja solo con el contrato y la documentación publicados por el proveedor. Una copia versionada queda en `docs/contracts/providers/`.
4. **Test de contrato:** valida nuestras solicitudes y sus respuestas de ejemplo contra el esquema publicado y falla ante un cambio incompatible (campo obligatorio nuevo, tipo cambiado, código de error nuevo).
5. **Tiempos y reintentos:** timeout de 2 s por llamada dentro del presupuesto de 5 s del gateway (D5). Un reintento solo si la operación es idempotente o el proveedor ofrece clave de idempotencia. Circuit breaker igual que en D10.
6. **Qué ve el usuario si el proveedor falla:** se decide según el flujo. La regla general es que el flujo principal se completa con la funcionalidad del proveedor degradada y un mensaje explícito ("no pudimos obtener X; reintentaremos" o "función no disponible momentáneamente"). Solo se bloquea si la capacidad es imprescindible para la decisión de negocio.
7. **Observabilidad:** métricas `provider_requests_total{outcome}` y `provider_request_duration_seconds`, y span de cliente en la traza.

## Consecuencias
Cuando se conozca el proveedor se actualizará este ADR con el flujo elegido, el comportamiento ante caída y la evidencia de la jornada de fallas. Si el contrato del proveedor cambia de versión, se registra un ADR nuevo que reemplace a este.
