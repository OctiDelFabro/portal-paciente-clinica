# ADR-012 — Balanceo de carga
**Decisión D12 · Estado: aceptada; implementación prevista para la presentación final.** Turnos corre en dos o más instancias. El gateway las balancea en round-robin, solo entre las sanas, con chequeos activos de `/healthz`.

## Contexto
El enunciado pide balanceo sobre al menos un servicio con dos o más instancias, detección de instancias caídas y evidencia de la distribución en la observabilidad. Turnos recibe los picos de demanda del dominio, y es el servicio que más se beneficia.

Turnos ya es apto para varias instancias:
- No guarda estado en memoria: las reservas se serializan con bloqueos en MySQL, no con mutex locales (D3/D4).
- El relay de outbox usa `FOR UPDATE SKIP LOCKED`, así dos instancias no publican el mismo evento.

## Decisión
| Aspecto | Decisión |
|---|---|
| Servicio | Turnos, con `deploy.replicas: 2` en compose (`turnos-1`, `turnos-2`). |
| Balanceador | El gateway, con lista de instancias por configuración (`TURNOS_URLS`). |
| Estrategia | Round-robin entre instancias sanas. |
| Detección | Chequeo activo de `GET /healthz` cada 2 s, timeout 500 ms. Dos fallas seguidas la marcan caída y una respuesta OK la reincorpora. También se marca caída si una solicitud real falla por conexión. |
| Ante la caída de una instancia | Las nuevas solicitudes van a las demás. Un GET en vuelo se reintenta en otra instancia; un POST no (D5) y el usuario ve un resultado indeterminado (504). |
| Evidencia | `gateway_upstream_requests_total{upstream,instance}` y `gateway_upstream_healthy{instance}` en Grafana; el campo `instance` de los logs de Turnos. |

## Alternativas consideradas
- **DNS round-robin de Docker:** no detecta instancias caídas y no muestra la distribución.
- **NGINX o Traefik delante de Turnos:** una opción válida y probada, pero agrega otro componente y su configuración. El gateway ya existe, ya mide y ya propaga trazas. Si el balanceo en el gateway resulta frágil, se reemplaza por Traefik en un ADR nuevo.

## Consecuencias
El gateway pasa a tener lógica de balanceo y es un punto único de falla (aceptado para el TP; en la nube se replicaría detrás del balanceador del proveedor). El rate limit de Notificaciones es por instancia (ADR-008); por eso no se replica Notificaciones hasta mover ese contador a Redis.
