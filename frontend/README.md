# Frontend del portal

React 19, TypeScript y Vite. Implementa registro/login, búsqueda y disponibilidad, reserva/cancelación, turnos paginados, historia personal y agenda/notas del profesional. Recepción verifica pacientes y vínculos; administración edita perfiles y reconstruye búsqueda. El médico puede consultar la historia desde el formulario de atención, con autorización en backend.

Requiere Node.js 22, npm y el backend levantado. Desde esta carpeta:

```sh
npm ci
npm run dev
npm test
npm run build
npx playwright install chromium
npm run test:ui
```

Vite escucha en http://127.0.0.1:5173 y redirige `/api` al gateway en http://127.0.0.1:8080. El build genera `dist/`; para servirlo fuera de Vite, configurar el mismo proxy `/api` en el servidor web y el fallback de la SPA. El despliegue público de socios en `8088` no ofrece el portal.

Los contratos HTTP se adaptan en `src/api/endpoints.ts`: Médicos envía `id`, convertido a `doctorId` en búsqueda y detalle; Turnos devuelve `{appointments, page, nextPage}`. `nextPage=0` indica el final. Las pantallas cargan más páginas y consultan cambios cada 15 s. `src/api/http.ts` mantiene el timeout de 8 s hasta terminar de leer el JSON. Una respuesta tardía de una sesión anterior no cierra una sesión nueva.

Las pruebas Node empaquetan el cliente TypeScript real con esbuild y comprueban los contratos, el timeout de cuerpo y el cambio de sesión. La prueba `test:ui` recorre la SPA compilada en Chromium con respuestas HTTP simuladas (paciente, médico, recepción y administración). No sustituyen una prueba del stack completo. Ver [verificación](../docs/VERIFICACION.md) y [pendientes](../docs/CORRECCIONES-REVISION.md).

El registro público siempre crea pacientes. Los roles de personal provienen del JWT emitido por Pacientes; escribir un rol en un header del navegador no otorga permisos. No guardar API keys de socios, del proveedor ni de servicios internos en el código web.

La auditoría funcional incorpora selector de menor, reprogramación, bandeja de avisos, configuración/bloqueos del médico y registro/edición/asignación por recepción. [Reglas y endpoints](../docs/AUDITORIA-FUNCIONAL-CORRECCIONES.md). El buscador obtiene el catálogo filtrado completo (máximo 1000), ordena con disponibilidad real y luego pagina. Fechas disponibles: 60 días desde mañana en Argentina.
