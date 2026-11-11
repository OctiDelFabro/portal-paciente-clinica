import { test, after } from "node:test";
import assert from "node:assert/strict";
import { build } from "esbuild";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const dir = await mkdtemp(join(tmpdir(), "portal-api-"));
await build({
  stdin: {
    contents:
      "export * from './src/api/http'; export * from './src/api/endpoints';",
    resolveDir: process.cwd(),
    loader: "ts",
  },
  bundle: true,
  platform: "node",
  format: "esm",
  outfile: join(dir, "api.mjs"),
});
const api = await import(pathToFileURL(join(dir, "api.mjs")));
globalThis.window = globalThis;
after(() => rm(dir, { recursive: true, force: true }));
const json = (data) =>
  new Response(JSON.stringify(data), {
    headers: { "Content-Type": "application/json" },
  });
test("real doctor id reaches availability, for search and detail", async () => {
  const profile = {
    id: "doc-001",
    fullName: "Laura Gómez",
    specialties: ["cardiologia"],
    acceptsMinors: false,
    active: true,
  };
  const urls = [];
  globalThis.fetch = async (url) => {
    urls.push(url);
    return json(
      url.includes("/search?")
        ? { items: [profile], page: 1, size: 8, total: 1 }
        : url.includes("/availability?")
          ? { slots: [] }
          : profile,
    );
  };
  const page = await api.medicos.search({
    q: "",
    specialty: "",
    acceptsMinors: false,
    sort: "name",
    page: 1,
    size: 8,
  });
  assert.equal(page.items[0].doctorId, "doc-001");
  assert.equal((await api.medicos.doctor("doc-001")).doctorId, "doc-001");
  await api.turnos.availability(page.items[0].doctorId, "2026-11-12");
  assert.match(urls.at(-1), /doctors\/doc-001\/availability/);
});
test("appointments envelope retains the patients and doctors appointments", async () => {
  globalThis.fetch = async () =>
    json({
      appointments: [{ id: "a-1", patientId: "pat-001", doctorId: "doc-001" }],
    });
  assert.equal((await api.turnos.list())[0].id, "a-1");
  globalThis.fetch = async () => json({ unexpected: [] });
  await assert.rejects(api.turnos.list(), (e) => e.code === "BAD_RESPONSE");
});
test("timeout also aborts a stalled response body", async () => {
  globalThis.fetch = async (_url, { signal }) =>
    new Response(
      new ReadableStream({
        start(controller) {
          signal.addEventListener(
            "abort",
            () => controller.error(new Error("aborted")),
            { once: true },
          );
        },
      }),
      { headers: { "Content-Type": "application/json" } },
    );
  await assert.rejects(
    api.request("GET", "/slow", undefined, 15),
    (e) => e.code === "TIMEOUT",
  );
});
test("a late unauthorized reply cannot end a newer session", async () => {
  let current = "old";
  let logouts = 0;
  let resolve;
  api.configureHttp(
    () => current,
    () => logouts++,
  );
  globalThis.fetch = () =>
    new Promise((r) => {
      resolve = r;
    });
  const pending = api.request("GET", "/old");
  current = "new";
  resolve(
    new Response("{}", {
      status: 401,
      headers: { "Content-Type": "application/json" },
    }),
  );
  await assert.rejects(pending);
  assert.equal(logouts, 0);
  api.configureHttp(
    () => null,
    () => {},
  );
});
test("reception/dependent filters and reschedule endpoint retain target scope", async () => {
  const requests = [];
  globalThis.fetch = async (url, options) => {
    requests.push({ url, options });
    return json({ appointments: [], notices: [] });
  };
  await api.turnos.page(2, { patientId: "pat-child", doctorId: "doc-1" });
  const query = new URL(requests.at(-1).url, "http://test").searchParams;
  assert.equal(query.get("patientId"), "pat-child");
  assert.equal(query.get("doctorId"), "doc-1");
  assert.equal(query.get("page"), "2");
  await api.turnos.book("pat-child", "doc-1", "2026-12-01T09:00:00-03:00");
  assert.equal(JSON.parse(requests.at(-1).options.body).patientId, "pat-child");
  await api.turnos.reschedule("a-1", "2026-12-02T09:00:00-03:00");
  assert.match(requests.at(-1).url, /appointments\/a-1\/reschedule$/);
  assert.deepEqual(JSON.parse(requests.at(-1).options.body), {
    startAt: "2026-12-02T09:00:00-03:00",
  });
  await api.notifications.page(1, "pat-child");
  assert.equal(
    new URL(requests.at(-1).url, "http://test").searchParams.get("patientId"),
    "pat-child",
  );
});
test("catalog loads every matching page before the screen orders availability", async () => {
  const calls = [];
  globalThis.fetch = async (raw) => {
    const u = new URL(raw, "http://test");
    calls.push(u);
    const page = Number(u.searchParams.get("page"));
    return json({
      items: Array.from({ length: page === 1 ? 50 : 1 }, (_, i) => ({
        id: `doc-${(page - 1) * 50 + i}`,
        fullName: "Profesional",
        specialties: [],
      })),
      total: 51,
      page,
      size: 50,
    });
  };
  const r = await api.medicos.catalog({
    q: "",
    specialty: "",
    acceptsMinors: false,
    sort: "nextAvailable",
    page: 3,
    size: 8,
  });
  assert.equal(r.items.length, 51);
  assert.equal(calls.length, 2);
  assert.equal(calls[0].searchParams.get("sort"), "name");
  assert.equal(calls[1].searchParams.get("page"), "2");
});
