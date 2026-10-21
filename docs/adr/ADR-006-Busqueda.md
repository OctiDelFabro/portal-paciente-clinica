# ADR-006 — Búsqueda de médicos
**Decisión D6 · Estado: aceptada; implementación prevista para la presentación final.** OpenSearch indexa los perfiles de médicos de forma derivada desde Médicos (MySQL), sincronizado por eventos, con reconstrucción completa disponible.

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
- Paginación: `size` ≤ 50, `from + size` ≤ 1000; más allá se usa `search_after`.
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
