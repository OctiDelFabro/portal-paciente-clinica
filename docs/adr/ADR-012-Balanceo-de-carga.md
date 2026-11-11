# ADR-012 — Balanceo de carga
**Decisión D12 · Estado: implementada en el gateway para la presentación final, con las desviaciones indicadas abajo.** Turnos corre en dos o más instancias. El gateway las balancea en round-robin, solo entre las sanas, con chequeos activos de `/healthz`.

## Contexto
El enunciado pide balanceo sobre al menos un servicio con dos o más instancias, detección de instancias caídas y evidencia de la distribución en la observabilidad. Turnos recibe los picos de demanda del dominio, y es el servicio que más se beneficia.

Turnos ya es apto para varias instancias:
- No guarda estado en memoria: las reservas se serializan con bloqueos en MySQL, no con mutex locales (D3/D4).
- El relay de outbox usa `FOR UPDATE SKIP LOCKED`, así dos instancias no publican el mismo evento.

## Decisión
| Aspecto | Decisión |
|---|---|
| Servicio | Turnos, con dos servicios en compose (`turnos-1` y `turnos-2`) que comparten configuración mediante un anclaje YAML (`<<: *turnos`). |
| Balanceador | El gateway, con lista de instancias por configuración (`TURNOS_URLS`). |
| Estrategia | Round-robin entre instancias sanas. |
| Detección | Chequeo activo de `GET /healthz` cada 2 s, timeout 500 ms. Dos fallas seguidas la marcan caída y una respuesta OK la reincorpora. También se marca caída si una solicitud real falla por conexión. |
| Ante la caída de una instancia | Las nuevas solicitudes van a las demás. Un GET en vuelo se reintenta en otra instancia; un POST no (D5) y el usuario ve un resultado indeterminado (504). |
| Evidencia | `gateway_upstream_requests_total{upstream,instance}` y `gateway_upstream_healthy{instance}` en Grafana; el campo `instance` de los logs de Turnos. |

## Implementación y desviaciones
`gateway/cmd/proxy.go`: un único proxy (`poolProxy`) para todas las rutas; un servicio con una sola URL es un pool de una instancia. El pool de Turnos es compartido por `/api/turnos/*` y por las rutas de partners `/v1/appointments`, `/v1/doctors` y `/v1/usage`, así ambos listeners ven las mismas instancias y el mismo estado de salud.

- Configuración: `TURNOS_URLS=http://turnos-1:8081,http://turnos-2:8081`; si falta, se usa `TURNOS_URL`. La etiqueta `instance` de las métricas es `host:puerto` de cada URL.
- Estado inicial: cada instancia arranca como sana y el primer chequeo corre al iniciar.
- Desviación — todas caídas: si no queda ninguna instancia sana, el gateway intenta igual en round-robin sobre todas (*fail-open*), porque la marca puede tener hasta 2 s de atraso; la respuesta real decide (502/504 si siguen caídas).
- Desviación — falla de conexión: solo un error de conexión (dial rechazado, conexión rota) marca la instancia caída. Un timeout no, porque indica lentitud y no caída; el chequeo activo la saca si `/healthz` tampoco responde.
- Un POST que no llega a conectarse devuelve 502 (no se procesó); uno que se conectó y no respondió a tiempo, 504 indeterminado. Ninguno se reintenta.
- Logs: `gateway.instance_down` (WARN) y `gateway.instance_up` (INFO) con `upstream` y `upstream_instance` al cambiar el estado, para fechar una caída sin consultar Prometheus.
- Evidencia: `gateway/cmd/balancer_test.go` (distribución 4/4 en portal y partners, dos fallas para bajar y un OK para volver, timeout del chequeo, marca por conexión, GET reintentado en otra instancia, POST con timeout no reintentado, *fail-open*).

## Alternativas consideradas
- **DNS round-robin de Docker:** no detecta instancias caídas y no muestra la distribución.
- **NGINX o Traefik delante de Turnos:** una opción válida y probada, pero agrega otro componente y su configuración. El gateway ya existe, ya mide y ya propaga trazas. Si el balanceo en el gateway resulta frágil, se reemplaza por Traefik en un ADR nuevo.

## Consecuencias
El gateway pasa a tener lógica de balanceo y es un punto único de falla (aceptado para el TP; en la nube se replicaría detrás del balanceador del proveedor). El rate limit de Notificaciones es por instancia (ADR-008); por eso no se replica Notificaciones hasta mover ese contador a Redis.
