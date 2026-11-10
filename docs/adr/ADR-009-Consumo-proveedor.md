# ADR-009 — Consumo de la capacidad del proveedor

Decisión D9 aceptada e implementada: Turnos consume `ArquitecturasoftwareRenewableEnergy` para estimar los kWh de los equipos de una consulta. Contrato versionado: `docs/contracts/providers/renewable-energy-v1`.

## Decisión

- El puerto `EnergyEstimator` pertenece a la aplicación Turnos; `adapters/energyprovider` traduce solicitud, respuesta y errores. React y gateway no reciben la clave del proveedor.
- La estimación se ejecuta después del commit, fuera de la transacción y de los bloqueos. Presupuesto inline: 1.200 ms; reestimación en segundo plano: 5 s. El circuito abre tras cinco fallos consecutivos, durante 30 s. La estimación no se reintenta dentro del mismo pedido de reserva.
- Caída, timeout, circuito abierto o respuesta inválida dejan `energy.status=pending`. La reserva permanece confirmada. El worker reintenta lotes de hasta 20 turnos futuros pendientes cada minuto y termina el lote ante un fallo del proveedor.
- El cliente rechaza redirecciones, incluidas 307/308, para no reenviar `X-API-Key` a otro destino. Cambiar URL/origen requiere configuración explícita.
- El proveedor entrega kWh, no dinero. Turnos calcula ARS mediante tarifa configurable; cada turno conserva tanto precio base como tarifa por kWh al reservar, incluso si la estimación se completa después. Los turnos anteriores se completan una vez con la configuración vigente al actualizar; no puede reconstruirse un precio que no fue guardado.
- Métricas y trazas permiten distinguir éxito, rechazo, circuito y timeout. Las pruebas HTTP emplean servidores locales y datos sintéticos, incluidas redirecciones entre hosts.

## Operación y límites

`PROVIDER_ENERGY_URL` y `PROVIDER_ENERGY_API_KEY` configuran el adaptador. Compose usa el stub local de forma predeterminada. La presencia del contrato y las pruebas con stub no acredita una llamada al proveedor real; esa verificación requiere URL, credencial y coordinación del otro grupo. No se publican claves en documentación ni frontend.
