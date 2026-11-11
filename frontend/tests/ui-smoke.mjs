// A browser regression over the built SPA with service-shaped HTTP fixtures.
// This catches client contract and screen wiring failures, not live DB/broker failures.
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { extname, resolve, sep } from "node:path";
import { chromium } from "playwright";

const dist = resolve("dist");
const mime = {
  ".html": "text/html",
  ".js": "text/javascript",
  ".css": "text/css",
  ".woff": "font/woff",
  ".woff2": "font/woff2",
};
const server = createServer(async (req, res) => {
  try {
    const path = resolve(
      dist,
      "." + new URL(req.url, "http://localhost").pathname,
    );
    if (path !== dist && !path.startsWith(dist + sep)) {
      res.writeHead(403).end();
      return;
    }
    const file = path === dist ? resolve(dist, "index.html") : path;
    res.setHeader(
      "Content-Type",
      mime[extname(file)] ?? "application/octet-stream",
    );
    res.end(await readFile(file));
  } catch {
    res.writeHead(404).end();
  }
});
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
const base = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch({
  executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH || undefined,
  headless: true,
});
const errors = [];
const checks = [];
const doctor = {
  id: "doc-001",
  fullName: "Laura Gómez",
  specialties: ["cardiologia"],
  active: true,
  acceptsMinors: true,
};
const start = new Date(Date.now() + 4 * 86400000).toISOString();
const end = new Date(Date.parse(start) + 1800000).toISOString();
const price = {
  basePriceArs: 25000,
  energy: {
    status: "estimated",
    kWh: 0.381,
    costArs: 68.58,
    algorithmVersion: "consumo-v1",
  },
  totalArs: 25068.58,
};
let appointment = {
  id: "smoke-a-1",
  patientId: "pat-new",
  doctorId: doctor.id,
  startAt: start,
  endAt: end,
  status: "booked",
  version: 1,
  price,
};
let booked = false,
  enabled = false,
  role = "patient",
  notes = [];
let seenNextAvailable = false;
let seenDoctorURL = false,
  seenBooking = false,
  seenReason = "";
const token = () =>
  "e30." +
  Buffer.from(
    JSON.stringify({
      sub:
        role === "doctor"
          ? doctor.id
          : role === "patient"
            ? "pat-new"
            : role === "admin"
              ? "admin-001"
              : "rec-001",
      role,
      exp: Math.floor(Date.now() / 1000) + 3600,
    }),
  ).toString("base64url") +
  ".test";
const page = await browser.newPage();
page.setDefaultTimeout(10000);
page.on("pageerror", (error) => errors.push(error.message));
await page.route("**/api/**", async (route) => {
  const req = route.request(),
    url = new URL(req.url()),
    path = url.pathname;
  const json = (body, status = 200) =>
    route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify(body),
    });
  if (path.endsWith("/auth/login"))
    return json({
      accessToken: token(),
      tokenType: "Bearer",
      expiresIn: 3600,
      user: { fullName: "Demo", role },
    });
  if (path === "/api/medicos/specialties")
    return json({
      specialties: [{ code: "cardiologia", name: "Cardiología" }],
    });
  if (path === "/api/medicos/search")
    return json({ items: [doctor], page: 1, size: 8, total: 1 });
  if (path === "/api/medicos/doctors/doc-001") return json(doctor);
  if (path.endsWith("/next-available")) {
    assert.ok(path.includes("/doc-001/"));
    seenNextAvailable = true;
    return json({ doctorId: doctor.id, startAt: start, endAt: end, horizonDays: 60 });
  }
  if (path.includes("/availability")) {
    assert.ok(path.includes("/doc-001/"));
    seenDoctorURL = true;
    return json({
      doctorId: doctor.id,
      date: url.searchParams.get("date"),
      durationMinutes: 30,
      slots: [{ startAt: start, endAt: end }],
    });
  }
  if (path.includes("/quote"))
    return json({ doctorId: doctor.id, startAt: start, ...price });
  if (path === "/api/turnos/appointments" && req.method() === "POST") {
    const body = req.postDataJSON();
    assert.equal(body.doctorId, doctor.id);
    assert.equal(body.patientId, "pat-new");
    seenBooking = true;
    booked = true;
    return json(appointment, 201);
  }
  if (path === "/api/turnos/appointments")
    return json({
      appointments: booked ? [appointment] : [],
      page: 1,
      nextPage: 0,
    });
  if (path.endsWith("/cancel")) {
    seenReason = req.postDataJSON().reason;
    appointment = {
      ...appointment,
      status: "cancelled",
      version: 2,
      cancelReason: seenReason,
    };
    return json(appointment);
  }
  if (path.endsWith("/contact"))
    return json({ patientId: "pat-new", fullName: "Paciente nuevo" });
  if (path.endsWith("/notes")) {
    notes.push({
      id: "n-1",
      text: req.postDataJSON().text,
      createdAt: new Date().toISOString(),
    });
    return json(notes.at(-1), 201);
  }
  if (path.startsWith("/api/historia/records/"))
    return json({
      patientId: "pat-new",
      records: [
        {
          appointmentId: appointment.id,
          patientId: "pat-new",
          doctorId: doctor.id,
          startAt: start,
          status: "scheduled",
          notes,
        },
      ],
    });
  if (path === "/api/pacientes/patients")
    return json({
      patients: [
        {
          patientId: "pat-new",
          fullName: "Paciente nuevo",
          email: "new@example.test",
          enabled,
          responsiblePatientId: "",
        },
      ],
      page: 1,
      nextPage: 0,
    });
  if (path.endsWith("/verification")) {
    enabled = req.postDataJSON().enabled;
    return json({ enabled });
  }
  if (path.endsWith("/admin/reindex"))
    return json({ index: "doctors-v2", count: 2 });
  throw new Error(`Unexpected fixture request: ${req.method()} ${path}`);
});
async function login(nextRole) {
  role = nextRole;
  await page.goto(base);
  await page
    .getByLabel("Correo electrónico", { exact: true })
    .fill("demo@example.test");
  await page.getByLabel("Contraseña", { exact: true }).fill("test-password");
  await page.getByRole("button", { name: "Ingresar", exact: true }).click();
}
async function logout() {
  await page.getByRole("button", { name: "Salir", exact: true }).click();
}
try {
  await login("patient");
  await page
    .getByRole("button", { name: "Ver disponibilidad de Laura Gómez" })
    .click();
  // The radio is visually covered by its label; select the visible control
  // just as a user does, then verify the native checked state.
  await page.locator("label.slot").click();
  assert.ok(await page.locator('input[name="slot"]').isChecked());
  await page
    .getByRole("button", { name: "Confirmar turno", exact: true })
    .click();
  await page
    .getByRole("heading", { name: "Turno confirmado", exact: true })
    .waitFor();
  assert.ok(seenDoctorURL && seenBooking && seenNextAvailable);
  checks.push(
    "patient search -> availability -> booking uses actual doctor ID",
  );
  await page.getByRole("link", { name: "Mis turnos", exact: true }).click();
  await page.getByRole("button", { name: "Cancelar", exact: true }).click();
  const reason = "No puedo asistir por un problema de trabajo";
  await page.getByLabel("Motivo de la cancelación (opcional)").fill(reason);
  await page.getByRole("button", { name: /Cancelar turno N°/ }).click();
  await page
    .getByText("El horario quedó libre para otra persona.", { exact: true })
    .waitFor();
  assert.equal(seenReason, reason);
  checks.push(
    "appointments envelope renders and accepts a reason longer than 32 bytes",
  );
  await logout();
  appointment = { ...appointment, status: "booked" };
  await login("doctor");
  await page
    .getByRole("button", { name: "Registrar atención", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Consultar historia clínica", exact: true })
    .click();
  await page
    .getByRole("heading", { name: "Historia clínica", exact: true })
    .waitFor();
  await page
    .getByLabel("Nota de la atención", { exact: true })
    .fill("Control clínico de prueba.");
  await page.getByRole("button", { name: "Guardar nota", exact: true }).click();
  await page
    .getByText("Nota guardada en la historia de Paciente nuevo", {
      exact: true,
    })
    .waitFor();
  assert.equal(notes.length, 1);
  checks.push("doctor agenda -> authorized timeline screen -> add note");
  await logout();
  await login("reception");
  await page
    .getByRole("button", { name: "Verificar y habilitar", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Inhabilitar", exact: true })
    .waitFor();
  assert.equal(enabled, true);
  checks.push("reception login -> patient verification screen");
  await logout();
  await login("admin");
  await page.getByLabel("Identificador del profesional").fill("doc-001");
  await page
    .getByRole("button", { name: "Consultar perfil", exact: true })
    .click();
  await page.getByLabel("Nombre", { exact: true }).waitFor();
  checks.push("admin login -> doctor profile administration");
  assert.deepEqual(errors, []);
  for (const check of checks) console.log("OK", check);
  console.log(
    `${checks.length} browser flows passed; HTTP fixtures, not live infrastructure.`,
  );
} finally {
  await browser.close();
  await new Promise((resolve) => server.close(resolve));
}
