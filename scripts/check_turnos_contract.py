"""Prueba de contrato de la capacidad publicada de Turnos (turnos-partner-v1).

Corre contra el stack levantado (docker compose up) a traves del gateway y
valida estados, headers y cuerpos con el OpenAPI. No sustituye un validador
completo de OpenAPI/JSON Schema.

Variables:
  TURNOS_BASE_URL   default http://127.0.0.1:8080
  TURNOS_KEY        clave de un consumidor de TURNOS_PARTNER_KEYS (obligatoria)
  TURNOS_KEY_OTHER  clave de otro consumidor, para probar aislamiento (opcional)

Cada corrida reserva y cancela un turno en una fecha aleatoria lejana para no
chocar con corridas anteriores; la reserva queda cobrada en /v1/usage.
"""
import datetime
import json
import os
import pathlib
import random
import re
import sys
import urllib.error
import urllib.request
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
CONTRACT = json.loads((ROOT / "docs/contracts/turnos-partner-v1.openapi.json").read_text(encoding="utf-8"))
BASE = os.environ.get("TURNOS_BASE_URL", "http://127.0.0.1:8080").rstrip("/")
KEY = os.environ.get("TURNOS_KEY")
OTHER = os.environ.get("TURNOS_KEY_OTHER")
checks = []


def resolve(schema):
    while "$ref" in schema:
        target = CONTRACT
        for part in schema["$ref"].split("/")[1:]:
            target = target[part]
        schema = target
    return schema


def schema_check(value, schema):
    schema = resolve(schema)
    if "enum" in schema:
        assert value in schema["enum"], ("enum", value)
    typ = schema.get("type")
    if typ == "object":
        assert isinstance(value, dict), "object"
        missing = set(schema.get("required", [])) - set(value)
        assert not missing, ("required", missing)
        props = schema.get("properties", {})
        if schema.get("additionalProperties") is False:
            assert set(value) <= set(props), "additionalProperties"
        for key, item in value.items():
            if key in props:
                schema_check(item, props[key])
    elif typ == "array":
        assert isinstance(value, list), "array"
        for item in value:
            schema_check(item, schema["items"])
    elif typ == "string":
        assert isinstance(value, str), "string"
        assert len(value) >= schema.get("minLength", 0), "minLength"
        if schema.get("pattern"):
            assert re.search(schema["pattern"], value), ("pattern", value)
        if schema.get("format") == "date-time":
            parsed = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
            assert parsed.tzinfo, "timezone"
    elif typ == "integer":
        assert isinstance(value, int) and not isinstance(value, bool), "integer"
        assert value >= schema.get("minimum", value), "minimum"


def request(method, path, body=None, key=KEY, idem=None, media="application/json"):
    headers = {"Content-Type": media, "X-Correlation-ID": "contract-" + uuid.uuid4().hex[:12]}
    if key is not None:
        headers["X-API-Key"] = key
    if idem is not None:
        headers["Idempotency-Key"] = idem
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        res = urllib.request.urlopen(req, timeout=5)
    except urllib.error.HTTPError as err:
        res = err
    with res:
        return res.status, {k.lower(): v for k, v in res.headers.items()}, json.loads(res.read())


def route_of(path):
    path = path.split("?")[0]
    if path.endswith("/cancel"):
        return "/v1/appointments/{id}/cancel"
    if path.startswith("/v1/appointments/"):
        return "/v1/appointments/{id}"
    return path


def verify(label, method, path, expected, code=None, **kwargs):
    status, headers, body = request(method, path, **kwargs)
    assert status == expected, (label, status, expected, body)
    declared = resolve(CONTRACT["paths"][route_of(path)][method.lower()]["responses"][str(expected)])
    schema_check(body, declared["content"]["application/json"]["schema"])
    if code is not None:
        assert body["error"]["code"] == code, (label, body)
    assert headers.get("cache-control") == "no-store", (label, "cache-control")
    checks.append(label)
    return headers, body


def run():
    if not KEY:
        sys.exit("Definir TURNOS_KEY con una clave de TURNOS_PARTNER_KEYS.")
    for example in sorted((ROOT / "docs/contracts/examples/turnos").glob("*.json")):
        value = json.loads(example.read_text(encoding="utf-8"))
        name = "Error" if example.name.startswith("error-") else "BookingRequest"
        schema_check(value, CONTRACT["components"]["schemas"][name])
    checks.append("ejemplos publicados validos")

    verify("autenticacion requerida", "GET", "/v1/doctors", 401, "UNAUTHORIZED", key=None)
    verify("clave invalida", "GET", "/v1/doctors", 401, "UNAUTHORIZED", key="not-a-key")
    _, catalog = verify("catalogo de medicos", "GET", "/v1/doctors", 200)
    doctor = catalog["doctors"][0]

    now = datetime.datetime.now(datetime.timezone.utc)
    start = (now + datetime.timedelta(days=random.randint(60, 3000))).replace(hour=random.randint(8, 18), minute=0, second=0, microsecond=0)
    booking = {"patientId": "pat-001", "doctorId": doctor["id"], "startAt": start.strftime("%Y-%m-%dT%H:%M:%SZ")}
    key = "contract-" + uuid.uuid4().hex

    verify("Idempotency-Key obligatoria", "POST", "/v1/appointments", 400, "INVALID_IDEMPOTENCY_KEY", body=booking)
    verify("Content-Type obligatorio", "POST", "/v1/appointments", 415, "UNSUPPORTED_MEDIA_TYPE", body=booking, idem=key, media="text/plain")
    soon = dict(booking, startAt=(now + datetime.timedelta(hours=2)).strftime("%Y-%m-%dT%H:%M:%SZ"))
    verify("anticipacion minima", "POST", "/v1/appointments", 422, "LEAD_TIME_TOO_SHORT", body=soon, idem=key)
    verify("paciente no habilitado", "POST", "/v1/appointments", 403, "PATIENT_NOT_ENABLED", body=dict(booking, patientId="pat-002"), idem=key)

    headers, booked = verify("reserva creada", "POST", "/v1/appointments", 201, body=booking, idem=key)
    assert headers["location"] == "/v1/appointments/" + booked["id"] and headers["idempotency-replayed"] == "false"
    assert booked["status"] == "booked" and booked["version"] == 1
    headers, replay = verify("repeticion devuelve el mismo turno", "POST", "/v1/appointments", 201, body=booking, idem=key)
    assert replay["id"] == booked["id"] and headers["idempotency-replayed"] == "true"
    moved = dict(booking, startAt=(start + datetime.timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ"))
    verify("misma clave con otros datos", "POST", "/v1/appointments", 409, "IDEMPOTENCY_CONFLICT", body=moved, idem=key)
    verify("horario ocupado", "POST", "/v1/appointments", 409, "SLOT_UNAVAILABLE", body=booking, idem=key + "-b")

    _, current = verify("consulta del turno", "GET", "/v1/appointments/" + booked["id"], 200)
    assert current["id"] == booked["id"]
    verify("turno inexistente", "GET", "/v1/appointments/does-not-exist", 404, "NOT_FOUND")
    if OTHER:
        verify("aislamiento entre consumidores", "GET", "/v1/appointments/" + booked["id"], 404, "NOT_FOUND", key=OTHER)
        verify("cancelacion ajena rechazada", "POST", "/v1/appointments/" + booked["id"] + "/cancel", 404, "NOT_FOUND", key=OTHER)

    _, usage = verify("estado de cuenta del mes", "GET", "/v1/usage", 200)
    items = [i for i in usage["items"] if i["appointmentId"] == booked["id"]]
    assert len(items) == 1 and items[0]["priceCents"] == usage["currentPrice"]["amountCents"], usage
    verify("periodo invalido", "GET", "/v1/usage?period=2026-13", 400, "INVALID_PERIOD")

    _, cancelled = verify("cancelacion propia", "POST", "/v1/appointments/" + booked["id"] + "/cancel", 200)
    assert cancelled["status"] == "cancelled" and cancelled["version"] == 2
    verify("cancelacion repetida", "POST", "/v1/appointments/" + booked["id"] + "/cancel", 409, "ALREADY_CANCELLED")
    _, after = verify("la cancelacion no descuenta el cobro", "GET", "/v1/usage", 200)
    assert [i["status"] for i in after["items"] if i["appointmentId"] == booked["id"]] == ["cancelled"]

    for label in checks:
        print("OK", label)
    print(f"{len(checks)} verificaciones contra {BASE}")


if __name__ == "__main__":
    run()
