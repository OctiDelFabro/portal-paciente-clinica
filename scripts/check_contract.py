"""Prueba HTTP independiente del mock. Valida los campos usados del contrato OpenAPI.
No sustituye un validador completo de OpenAPI/JSON Schema."""
import datetime
import json
import os
import pathlib
import re
import socket
import subprocess
import time
import urllib.error
import urllib.request
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
CONTRACT = json.loads((ROOT / "docs/contracts/notifications-v1.openapi.json").read_text(encoding="utf-8"))
checks = []

def schema_check(value, schema):
    if "$ref" in schema:
        target = CONTRACT
        for part in schema["$ref"].split("/")[1:]:
            target = target[part]
        return schema_check(value, target)
    if "oneOf" in schema:
        matches = 0
        for choice in schema["oneOf"]:
            try:
                schema_check(value, choice)
                matches += 1
            except AssertionError:
                pass
        assert matches == 1, "oneOf"
        return
    if "const" in schema:
        assert value == schema["const"], "const"
    if "enum" in schema:
        assert value in schema["enum"], "enum"
    typ = schema.get("type")
    if typ == "object":
        assert isinstance(value, dict), "object"
        assert set(schema.get("required", [])) <= set(value), "required"
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
        assert len(value) <= schema.get("maxLength", float("inf")), "maxLength"
        if schema.get("pattern"):
            assert re.search(schema["pattern"], value), "pattern"
        if schema.get("format") == "uuid":
            uuid.UUID(value)
        elif schema.get("format") == "date-time":
            parsed = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
            assert parsed.tzinfo, "timezone"
        elif schema.get("format") == "email":
            assert re.fullmatch(r"[^\s@]+@[^\s@]+\.[^\s@]+", value), "email"
    elif typ == "boolean":
        assert isinstance(value, bool), "boolean"

def request(port, method, path, body=None, key="demo-a", idem=None, media="application/json", raw=None):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    headers = {"Content-Type": media}
    if key is not None:
        headers["X-API-Key"] = key
    if idem is not None:
        headers["Idempotency-Key"] = idem
    req = urllib.request.Request(f"http://127.0.0.1:{port}{path}", data=data, headers=headers, method=method)
    try:
        res = urllib.request.urlopen(req, timeout=3)
    except urllib.error.HTTPError as err:
        res = err
    with res:
        return res.status, {k.lower(): v for k, v in res.headers.items()}, json.loads(res.read())

def verify_response(label, port, method, path, expected, **kwargs):
    status, headers, body = request(port, method, path, **kwargs)
    assert status == expected, (label, status, expected, body)
    route = "/v1/notifications/{id}" if path.startswith("/v1/notifications/") else path
    declared = CONTRACT["paths"][route][method.lower()]["responses"][str(expected)]
    schema_check(body, declared["content"]["application/json"]["schema"])
    assert "x-request-id" in headers, label
    checks.append(label)
    return headers, body

def launch(binary, result):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    env = dict(os.environ, MOCK_ADDR=f"127.0.0.1:{port}", MOCK_API_KEYS="team-a:demo-a,team-b:demo-b", MOCK_DELAY="0s", MOCK_RESULT=result)
    proc = subprocess.Popen([str(binary)], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            raise RuntimeError("El mock termino antes de iniciar.")
        try:
            request(port, "GET", "/healthz", key=None)
            return proc, port
        except (OSError, urllib.error.URLError):
            time.sleep(0.05)
    proc.terminate()
    raise RuntimeError("El mock no inicio.")

def run():
    cache = ROOT / ".cache"
    cache.mkdir(exist_ok=True)
    binary = cache / ("mock-check.exe" if os.name == "nt" else "mock-check")
    env = dict(os.environ, GOCACHE=str(cache / "go-build"), GOPATH=str(cache / "go"), GOPROXY="off", GOSUMDB="off")
    subprocess.run(["go", "build", "-o", str(binary), "./services/notifications/mock"], cwd=ROOT, env=env, check=True)
    fixtures = [json.loads(p.read_text(encoding="utf-8")) for p in sorted((ROOT / "docs/contracts/examples").glob("*.json"))]
    for fixture in fixtures:
        schema_check(fixture, CONTRACT["components"]["schemas"]["NotificationRequest"])
    proc, port = launch(binary, "sent")
    try:
        verify_response("health sin autenticacion", port, "GET", "/healthz", 200, key=None)
        _, catalog = verify_response("catalogo de tres plantillas", port, "GET", "/v1/templates", 200)
        assert len(catalog["templates"]) == 3
        for idx, fixture in enumerate(fixtures):
            _, n = verify_response(f"aceptacion de plantilla {idx+1}", port, "POST", "/v1/notifications", 202, body=fixture, idem=f"template-{idx}")
            assert n["status"] == "accepted" and n["simulated"]
        fixture = fixtures[0]
        headers, accepted = verify_response("POST aceptado", port, "POST", "/v1/notifications", 202, body=fixture, idem="same")
        assert headers["location"] == "/v1/notifications/" + accepted["id"]
        headers, replay = verify_response("replay conserva ID y respuesta", port, "POST", "/v1/notifications", 202, body=fixture, idem="same")
        assert replay == accepted and headers["idempotency-replayed"] == "true"
        changed = json.loads(json.dumps(fixture))
        changed["parameters"]["reference"] = "OTHER"
        verify_response("clave con datos distintos", port, "POST", "/v1/notifications", 409, body=changed, idem="same")
        _, state = verify_response("resultado simulado", port, "GET", "/v1/notifications/" + accepted["id"], 200)
        assert state["status"] == "sent" and state["simulated"]
        verify_response("aislamiento entre consumidores", port, "GET", "/v1/notifications/" + accepted["id"], 404, key="demo-b")
        verify_response("autenticacion requerida", port, "GET", "/v1/templates", 401, key=None)
        verify_response("JSON invalido", port, "POST", "/v1/notifications", 400, raw=b"{", idem="bad-json")
        verify_response("clave de idempotencia requerida", port, "POST", "/v1/notifications", 400, body=fixture)
        bad = dict(fixture, templateId="unknown")
        verify_response("plantilla inexistente", port, "POST", "/v1/notifications", 422, body=bad, idem="bad-template")
        verify_response("tipo de contenido", port, "POST", "/v1/notifications", 415, body=fixture, idem="bad-media", media="text/plain")
        verify_response("limite de cuerpo", port, "POST", "/v1/notifications", 413, raw=b" " * 16385, idem="big")
    finally:
        proc.terminate()
        proc.wait(timeout=5)
    proc, port = launch(binary, "failed")
    try:
        _, n = verify_response("aceptacion antes de fallo", port, "POST", "/v1/notifications", 202, body=fixtures[0], idem="fail")
        _, n = verify_response("fallo terminal simulado", port, "GET", "/v1/notifications/" + n["id"], 200)
        assert n["status"] == "failed" and n["simulated"]
    finally:
        proc.terminate()
        proc.wait(timeout=5)
    report = {"executedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(), "passed": len(checks), "checks": checks, "scope": "HTTP real en loopback, respuestas verificadas contra el subconjunto de schema usado; no validador completo de OpenAPI.", "limits": ["Sin correo real", "Sin bases", "Sin RabbitMQ", "Sin despliegue publico"]}
    output = ROOT / "docs/verification-http.json"
    output.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(json.dumps(report, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    run()

