# Verificación y límites de evidencia

Revisión del 10/11/2026 sobre una rama alternativa creada desde `main` (`d1c46b0b8375f8af4c49fb838f5be0c0e6159feb`). Ver [matriz de correcciones](CORRECCIONES-REVISION.md) para F01–F17. No se modificó el despliegue público.

## Comprobaciones reproducibles

```sh
go test -race ./...
go vet ./...
go test -tags integration -run '^$' ./...  # SOLO compilación
go test -tags integration -p 1 -timeout 15m ./...  # necesita Docker
cd frontend
npm ci
npm test
npm run build
npx playwright install chromium
npm run test:ui
```

La suite unitaria cubre contratos HTTP, autorización y habilitación, contacto de cuentas nuevas/responsables, restricciones de agenda, reindexación tras fallos, conexión SQL única, límites de memoria, motivos Unicode y precios conservados. El cliente TypeScript real se empaqueta para probar IDs y envelopes, timeout del cuerpo y respuestas tardías de sesiones anteriores.

Las pruebas de integración con Docker cubren MySQL, RabbitMQ, MongoDB, Redis y OpenSearch. Se agregaron casos de publicación antes de que inicien los consumidores, listas con más de 100 cancelados que no deben ocultar un turno activo, tarifa persistida y motivo de 200 caracteres Unicode. Los fixtures de reserva usan días laborables, horarios de atención y pacientes conocidos/habilitados.

El entorno de edición no dispone de Docker. **Compilación de integración no significa ejecución de integración.** `.github/workflows/verify.yml` ejecuta unitarias con detector de carreras, análisis Go, tests/build de React e integración real en GitHub Actions; consultar el resultado concreto del PR antes de integrar. En la ejecución [42893377415](https://github.com/OctiDelFabro/portal-paciente-clinica/actions/runs/42893377415), ninguno de los tres jobs llegó a iniciar: GitHub informó un bloqueo de cuenta por facturación. Los estados rojos de esa ejecución no son fallos de tests ejecutados.

## Recorrido manual con el stack local

1. Iniciar Compose y React como indica el README; configurar las dos claves internas/JWT y contraseñas locales.
2. Registrar una cuenta nueva e iniciar sesión. Reserva e historia deben rechazarse antes de la habilitación.
3. Iniciar sesión como recepción y verificar la cuenta. Su JWT de paciente previo puede reservar después de esa verificación.
4. Buscar “pediatría” y seleccionar un profesional. La consulta de disponibilidad debe contener su ID real, nunca `undefined`.
5. Reservar un horario ofrecido a más de 24 h. Ver el turno en la lista y el correo para la cuenta nueva en Mailpit. Verificar también el rechazo de un domingo o una hora fuera de la ventana mediante API.
6. Como médico asignado, consultar agenda, abrir la historia y registrar una nota. Como otro médico sin relación, comprobar 403. Como paciente habilitado, leer la nota. Como recepción, comprobar 403 en clínica.
7. Cancelar con un motivo de hasta 200 caracteres y comprobar lista, evento y correo. No repetir automáticamente escrituras cuando la respuesta indique resultado incierto: consultar primero su estado.
8. Detener y recuperar OpenSearch para verificar que la reentrega completa la indexación pendiente. En un broker vacío, publicar antes de iniciar consumidores y comprobar todas las colas.

## Mediciones anteriores

La versión anterior del documento reportaba p50 de disponibilidad con caché de 775 µs, hit rate 99,74% y p95 de reservas de 62,2 ms, además de un recorrido PowerShell contra contenedores. Esos números son **antecedentes reportados**, sin una nueva ejecución o trazas crudas validadas en esta revisión. No deben usarse para afirmar el rendimiento de esta rama. El script de carga de reservas ahora elige un día laborable y una hora válida; el anterior usaba un domingo fijo que la validación correcta rechaza.

No hay garantía general de cero deadlocks ni de cero errores durante failover. MySQL tiene reintentos de errores de bloqueo y el gateway puede recibir escrituras fallidas antes de retirar una réplica. El proveedor usa 1.200 ms inline, 5 s en segundo plano y circuito tras cinco fallos; las cifras antiguas de 150 ms/dos fallos eran incorrectas.

La API de socios de Hostinger se conserva desde `main`. Una comprobación de salud no prueba reserva/cancelación real ni integración con otro grupo. No se ejecutaron llamadas facturadas contra producción ni contra el proveedor real.
