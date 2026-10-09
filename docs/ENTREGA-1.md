# Estado de la Entrega 1
Referencia: enunciado, sección 6.2, entrega del viernes 9 de octubre.
Dominio aprobado: portal paciente de una clínica. La integración a consumir sigue pendiente de asignación de la cátedra.

| Exigencia | Estado del paquete |
|---|---|
| README inicial | Incluido, con alcance, comandos y estado real. |
| Alcance y requisitos | SPEC actualizado con las respuestas confirmadas. Permanecen preguntas de detalle identificadas. |
| Arquitectura | Incluida; cinco servicios, gateway, responsabilidades, datos y dependencias. |
| Diagramas de contexto y contenedores | Versiones actualizadas en Mermaid; los originales de Drive no fueron modificados. |
| Capacidad propia | Notificaciones: correo por plantillas generales, X-API-Key por consumidor. |
| Contrato versionado | OpenAPI 3.1.1 / API 1.0.0, errores, ejemplos y consulta de estado. |
| Mock | Ejecutable Go en memoria, sin correo real, con validación e idempotencia concurrente. |
| Estructura inicial de servicios y dependencias | Carpetas, código de arranque backend, base React/TypeScript y Compose inicial. |
| D1, D8, D3 y D5 | Cuatro archivos independientes incluidos; D3 y D5 son versiones iniciales. |
| Repositorio público accesible | Creado y verificado: https://github.com/OctiDelFabro/portal-paciente-clinica. Los 42 archivos de esta entrega están publicados en main. |

## Acciones para presentar
1. Utilizar el repositorio público existente OctiDelFabro/portal-paciente-clinica.
2. Utilizar el contenido publicado en main, conservando su estructura. No subir .env ni .cache.
3. Completar nombres de integrantes y comisión en README; el enlace del repositorio ya fue confirmado.
4. Ejecutar el mock en la computadora de presentación y demostrar catálogo → POST 202 → GET de estado → replay con el mismo ID.
5. Validar Docker y la instalación del frontend en un entorno operativo. Compose no acredita conexiones reales ni flujos clínicos.
6. Revisar y aceptar con el grupo los parámetros técnicos iniciales documentados en D5/D8.

La entrega 1 no se presenta como sistema clínico completo. El título del hito menciona mock; se entrega un mock comprobable aunque su lista específica no detalle el formato.

## Dudas restantes
- Proveedor de identidad y registro técnico de cuentas, cuentas de menores y múltiples responsables.
- Datos obligatorios de pacientes y procedimiento de verificación por recepción.
- Campos de atenciones y permisos para corregirlas; recepción no consulta contenido clínico.
- Campos, filtros, orden y paginación de la búsqueda.
- Cambios de horarios de atención que afecten reservas existentes.
- Recordatorio en una reserva efectuada exactamente 24 h antes: D5 propone omitir el aviso redundante junto a la confirmación; pendiente de acuerdo.
- Proveedor de correo, retención, respaldo y soporte de idempotencia del envío real.
- Grupo proveedor externo y capacidad que consumirá el portal.

## Continuidad
Los archivos originales de Drive se leyeron y se conservaron. La escritura fue bloqueada por la sesión, por lo que las actualizaciones de este paquete no están reflejadas allí. Este paquete es la versión que incorpora las últimas respuestas y el mock probado.

