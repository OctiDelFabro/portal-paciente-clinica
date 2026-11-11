# Diagramas actualizados
contexto.mmd y contenedores.mmd contienen Mermaid procesable. Representan el sistema implementado para la presentación final.
Incluyen frontend, propósito y transporte de las conexiones, propiedad de la agenda en Turnos y conexión explícita de Notificaciones a su base.
La capacidad externa consumida es la estimación de consumo de `ArquitecturasoftwareRenewableEnergy` (ADR-009). La identidad la emite el servicio Pacientes (JWT, ADR-014).
Los archivos diagrams.net originales de Drive no fueron editados. Para conservar ese formato, trasladar estos cambios a los originales antes de presentar. Mermaid puede previsualizarse en GitHub.


## Contexto

```mermaid
flowchart TB
 p["Paciente / responsable"]
 m["Médico"]
 r["Recepción"]
 a["Administración"]
 portal["Portal paciente de clínica"]
 consumidor["Car Cruds: servicio Empleados"]
 proveedor["ArquitecturasoftwareRenewableEnergy: estimación de consumo energético (Render)"]
 correo["Correo SMTP (Mailpit en compose)"]
 p -->|"Turnos e historia clínica"| portal
 m -->|"Agenda y atenciones"| portal
 r -->|"Pacientes, vínculos y turnos"| portal
 a -->|"Perfiles de personal y pacientes"| portal
 consumidor -->|"API HTTP de turnos (X-API-Key, facturada)"| portal
 portal -->|"HTTP: costo energético por turno (circuito + fallback)"| proveedor
 portal -->|"Envío de correos"| correo
```

## Contenedores

Actualizado para la presentación final: los seis componentes (gateway, Turnos, Pacientes, Médicos, Historia clínica y Notificaciones) están operativos, con outbox y DLQ, caché Redis, OpenSearch, proveedor externo y la pila de observabilidad.

```mermaid
flowchart TB
 usuarios["Pacientes, médicos, recepción y administración"] --> web["Frontend React + TypeScript"]
 web -->|"HTTP/JSON + JWT"| gw["API Gateway Go: valida JWT emitido por Pacientes"]
 consumidor["Servicio consumidor externo"] -->|"HTTP /v1 + X-API-Key"| gw
 subgraph servicios["Microservicios propios"]
  t["Turnos: agenda y reservas"]
  p["Pacientes: habilitación y vínculos"]
  m["Médicos: perfiles y especialidades"]
  h["Historia clínica: atenciones"]
  n["Notificaciones: plantillas y estados"]
 end
 gw -->|"Comandos y consultas"| t
 gw -->|"Datos y habilitación"| p
 gw -->|"Búsqueda de médicos"| m
 gw -->|"Consulta y registro clínico"| h
 gw -->|"API externa y avisos internos"| n
 t -->|"Validar habilitación y vínculos HTTP"| p
 t -->|"Validar profesional HTTP"| m
 h -->|"Validar vínculos HTTP"| p
 h -->|"Validar relación médico-paciente HTTP"| t
 n -->|"Obtener contactos HTTP"| p
 n -->|"Estado del turno HTTP"| t
 t -->|"Estimación energética HTTP (circuito)"| prov["Proveedor externo RenewableEnergy (Render)"]
 t -->|"Outbox → eventos turnos.*.v1"| mq["RabbitMQ portal.events.v1 + DLQ"]
 mq -->|"Consumo idempotente (inbox)"| n
 mq -->|"Eventos de turnos (inbox)"| h
 mq -->|"Indexación de perfiles"| m
 t --> dt[("MySQL Turnos")]
 p --> dp[("MySQL Pacientes")]
 m --> dm[("MySQL Médicos")]
 h --> dh[("MongoDB Historia clínica")]
 n --> dn[("MySQL Notificaciones")]
 t -->|"Caché de disponibilidad (cache-aside, TTL 30 s)"| redis["Redis"]
 m -->|"Proyección de perfiles"| os["OpenSearch"]
 n -->|"Correo SMTP"| correo["Mailpit (SMTP)"]
 subgraph obs["Observabilidad"]
  jaeger["Jaeger: trazas OTLP"]
  prom["Prometheus: métricas"]
  graf["Grafana: tablero"]
 end
 gw -.->|"Spans"| jaeger
 t -.->|"Spans"| jaeger
 n -.->|"Spans"| jaeger
 prom -.->|"Scrape /metrics"| gw
 prom -.->|"Scrape /metrics"| t
 prom -.->|"Scrape /metrics"| n
 graf -->|"Consultas PromQL"| prom

```
