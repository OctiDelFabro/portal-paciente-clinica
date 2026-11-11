# ADR-006 — Búsqueda de médicos
**Decisión D6 · Estado: implementada (presentación final), con los desvíos registrados al final.** OpenSearch indexa los perfiles de médicos de forma derivada desde Médicos (MySQL), sincronizado por eventos, con reconstrucción completa disponible.

## Contexto
RF-01: el paciente busca médicos y especialidades. El enunciado pide un motor de búsqueda con paginación, filtros y orden, con el índice sincronizado con los cambios y un retraso máximo definido. Médicos es dueño de los perfiles (D1); MySQL es la fuente autoritativa (D3).

## Decisión
**Índice `doctors-v1`** (alias `doctors`), un documento por médico:

| Campo | Tipo | Uso |
|---|---|---|
| `doctorId` | keyword | Identidad y deduplicación. |
| `fullName` | text + keyword | Búsqueda por nombre (texto completo) y orden alfabético. |
| `specialties` | keyword[] | Filtro principal del dominio. |
| `acceptsMinors` | boolean | Filtro para responsables que buscan para un menor (RF-05). |
| `active` | boolean | Excluye profesionales dados de baja. |
| `nextAvailableAt` | date | Orden "primero disponible"; proyección derivada de Turnos. |
| `profileVersion` | long | Descarta actualizaciones que llegan fuera de orden. |

**Consulta:** `GET /api/medicos/search?q=&specialty=&acceptsMinors=&sort=name|nextAvailable&page=&size=`
- Paginación: `size` ≤ 50, `from + size` ≤ 1000; más allá se responde 422 (no se implementó `search_after`).
- Orden: `name` (alfabético) o `nextAvailable` (más cercano primero).
- No se indexan datos de pacientes ni información clínica.

**Sincronización:**
1. Médicos guarda el cambio y `medicos.perfil-actualizado.v1` en su outbox, en la misma transacción (mismo patrón que Turnos, D5).
2. Un indexador consume el evento y hace upsert con `version_type=external` sobre `profileVersion`, así un evento viejo nunca pisa uno nuevo.
3. `nextAvailableAt` se actualiza consumiendo `turnos.reservado.v1` y `turnos.cancelado.v1`.
4. **Reconstrucción:** un comando recrea `doctors-vN+1` desde MySQL y cambia el alias en forma atómica. Se usa ante cambios de mapping o divergencia.

**Retraso máximo tolerable:** 5 s (p95) entre el commit en Médicos y la aparición en resultados.
Justificación: los perfiles cambian poco. Ver una especialidad desactualizada durante segundos no tiene consecuencias, porque la reserva siempre valida contra Turnos. Se mide con la métrica `search_index_lag_seconds` (diferencia entre `occurredAt` del evento y el momento de indexación).

## Alternativas consideradas
- **`LIKE` en MySQL:** no da relevancia ni texto completo y no cumple el requisito de motor de búsqueda.
- **Sincronizar al momento de buscar:** cada búsqueda pagaría el costo de leer MySQL y el índice dejaría de servir. Además, el enunciado exige mantenerlo sincronizado ante cambios en los datos.
- **Doble escritura desde la API (MySQL + OpenSearch):** si falla la segunda escritura el índice diverge sin rastro; la outbox deja el evento pendiente y reintentable.

## Consecuencias
- Consistencia eventual: un cambio tarda hasta el retraso acordado en aparecer.
- Si OpenSearch cae, la búsqueda responde 503 con un mensaje claro y el resto del portal sigue funcionando. Las reservas no dependen del índice.
- Operar OpenSearch tiene costo de memoria (512 MB en local).

## Implementación y desvíos
Código en `services/medicos` (ver su README). Se respetan el índice, el alias, los filtros, el orden, los límites de paginación, la outbox, la reconstrucción y la métrica `search_index_lag_seconds`. Desvíos respecto de lo decidido arriba:

| Desvío | Motivo |
|---|---|
| `version_type=external_gte` en lugar de `external`. | `nextAvailableAt` cambia sin que cambie el perfil. Con `external` estricto, una actualización de disponibilidad con el mismo `profileVersion` se rechazaría. `external_gte` igual impide que un perfil más viejo pise uno nuevo. |
| El indexador relee el estado actual en MySQL en vez de usar el contenido del evento. | Un evento repetido o fuera de orden termina escribiendo el último estado, así el consumidor es idempotente sin tabla inbox. |
| Más allá de `from + size` = 1000 se responde 422; no se implementó `search_after`. | Con el volumen de médicos de la clínica alcanza con refinar los filtros. Queda como deuda. |
| `nextAvailableAt` es aproximado: primer instante desde ahora + 24 h que no cae en un turno activo conocido. Ignora el horario de atención. | La agenda y los horarios pertenecen a Turnos (D1). Médicos arma una proyección local (`doctor_bookings`) con `turnos.reservado.v1` y `turnos.cancelado.v1` y no duplica la agenda. El valor se recalcula solo ante un evento o una reconstrucción, así que envejece con el paso del tiempo. Por eso el portal **no muestra** este valor: la columna «Próximo libre» consulta `GET /api/turnos/doctors/{id}/next-available`, que calcula Turnos con horario, anticipación de 24 h y superposición (horizonte de 60 días). El valor indexado sigue disponible para consumidores de Médicos. El portal descarga el catálogo filtrado por nombre (hasta 1000 resultados), consulta Turnos con cuatro solicitudes concurrentes, ordena el conjunto completo por próximo libre y luego pagina de a ocho. No usa el valor aproximado para ordenar entre páginas. |
| La reconstrucción existe como comando (`medicos reindex`) y como `POST /admin/reindex` (rol admin). En el primer arranque se crea `doctors-v1` sola. | Operación y demo sin acceso al contenedor. Un cambio confirmado entre la lectura de MySQL y el cambio de alias puede quedar fuera del índice nuevo hasta el próximo evento de ese médico o la próxima reconstrucción. |
| Los nombres y especialidades semilla los define Médicos; los IDs (`doc-001`, `doc-002`) son los mismos que acepta Turnos. | El semilla de Turnos solo tiene IDs y duraciones. Un test compara ambos conjuntos de IDs. |
| El relay de la outbox replica el de Turnos en lugar de compartirlo. | Está en `services/turnos/internal` y Go no permite importarlo desde otro servicio. Moverlo a `internal/platform` queda como deuda. |
