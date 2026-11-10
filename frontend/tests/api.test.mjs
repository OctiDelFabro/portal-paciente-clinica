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
