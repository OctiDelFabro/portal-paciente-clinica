# Diagramas actualizados
contexto.mmd y contenedores.mmd contienen Mermaid procesable. Representan el diseño, no componentes operativos.
Incluyen frontend, propósito y transporte de las conexiones, propiedad de la agenda en Turnos y conexión explícita de Notificaciones a su base.
La capacidad externa a consumir permanece pendiente de asignación; no se inventa un proveedor.
Los archivos diagrams.net originales de Drive no fueron editados. Para conservar ese formato, trasladar estos cambios a los originales antes de presentar. Mermaid puede previsualizarse en GitHub.


## Contexto

```mermaid
flowchart TB
 p["Paciente / responsable"]
 m["Médico"]
 r["Recepción"]
 portal["Portal paciente de clínica"]
 consumidor["Microservicio del grupo consumidor"]
 proveedor["Grupo proveedor: pendiente de asignación"]
 correo["Proveedor de correo: pendiente"]
 identidad["Proveedor de identidad: pendiente"]
 p -->|"Turnos e historia clínica"| portal
 m -->|"Agenda y atenciones"| portal
 r -->|"Pacientes, vínculos y turnos"| portal
 consumidor -->|"API HTTP de correos por plantillas"| portal
 portal -.->|"Capacidad a definir"| proveedor
 portal -->|"Envío de correos"| correo
 portal -->|"Autenticación"| identidad

```

## Contenedores

```mermaid
flowchart TB
 usuarios["Pacientes, médicos y recepción"] --> web["Frontend React + TypeScript"]
 web -->|"HTTP/JSON"| gw["API Gateway Go"]
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
 t -->|"Eventos de Turnos v1"| mq["RabbitMQ"]
 mq -->|"Consumo idempotente"| n
 t --> dt[("MySQL Turnos")]
 p --> dp[("MySQL Pacientes")]
 m --> dm[("MySQL Médicos")]
 h --> dh[("MongoDB Historia clínica")]
 n --> dn[("MySQL Notificaciones")]
 t -.->|"Lectura derivada: flujo por validar"| redis["Redis"]
 m -->|"Proyección de perfiles"| os["OpenSearch"]
 n -->|"Correo"| correo["Proveedor de correo pendiente"]
 gw -->|"Verificación de identidad"| idp["Proveedor de identidad pendiente"]

```
