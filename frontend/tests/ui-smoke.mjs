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
let catalog = [doctor],
  farSearch = false;
let expectedTarget = "pat-new",
  dependent = true,
  seenReschedule = false,
  scheduleSaved = false,
  noticeRead = false,
  profileEdited = false;
let rejectRescheduleOnce = true;
let patientCreated = false,
  linkSaved = false;
let schedule = {
    doctorId: doctor.id,
    durationMinutes: 30,
    weekdays: [1, 2, 3, 4, 5],
    open: "09:00",
    close: "18:00",
  },
  blocks = [];
const patient = () => ({
  patientId: "pat-new",
  fullName: "Paciente nuevo",
  email: "new@example.test",
  documentNumber: "12345678",
  birthDate: "1990-01-01",
  enabled,
  responsiblePatientId: "",
});
const child = () => ({
  patientId: "pat-child",
  fullName: "Menor de prueba",
  email: null,
  documentNumber: "98765432",
  birthDate: "2015-01-01",
  enabled: true,
  responsiblePatientId: "pat-new",
});
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
  if (path === "/api/pacientes/me")
    return json({ ...patient(), role: "patient" });
  if (path === "/api/pacientes/dependents")
    return json({ patients: dependent ? [child()] : [] });
  if (path.endsWith("/schedule")) {
    if (req.method() === "PUT") {
      schedule = req.postDataJSON();
      scheduleSaved = true;
    }
    return json(schedule);
  }
  if (path.endsWith("/release")) {
    blocks = [];
    return json({ released: true });
  }
  if (path.endsWith("/blocks")) {
    if (req.method() === "POST") {
      const b = { ...req.postDataJSON(), id: "block-1", doctorId: doctor.id };
      blocks.push(b);
      return json(b, 201);
    }
    return json({ blocks });
  }
  if (path === "/api/notifications/notices")
    return json({
      notices: [
        {
          id: "notice-1",
          patientId: expectedTarget,
          turnId: appointment.id,
          kind: "appointment_booked",
          doctorId: doctor.id,
          startAt: start,
          createdAt: new Date().toISOString(),
          ...(noticeRead ? { readAt: new Date().toISOString() } : {}),
        },
      ],
      page: 1,
      nextPage: 0,
    });
  if (path.endsWith("/notice-1/read")) {
    noticeRead = true;
    return json({ read: true });
  }
  if (path === "/api/medicos/specialties")
    return json({
      specialties: [{ code: "cardiologia", name: "Cardiología" }],
    });
  if (path === "/api/medicos/search")
    return json({ items: catalog, page: 1, size: 50, total: catalog.length });
  if (path === "/api/medicos/doctors/doc-001") return json(doctor);
  if (path.endsWith("/next-available")) {
    seenNextAvailable = true;
    const id = path.split("/").at(-2);
    const date = farSearch
      ? new Date(
          Date.now() +
            (id === "doc-001" ? 20 : id === "doc-010" ? 2 : 10) * 86400000,
        ).toISOString()
      : start;
    return json({
      doctorId: id,
      startAt: date,
      endAt: new Date(Date.parse(date) + 1800000).toISOString(),
      horizonDays: 60,
    });
  }
  if (path.includes("/availability")) {
    assert.ok(path.includes("/doc-001/"));
    seenDoctorURL = true;
    return json({
      doctorId: doctor.id,
      date: url.searchParams.get("date"),
      durationMinutes: 30,
      slots: [
        {
          startAt: `${url.searchParams.get("date")}T09:00:00-03:00`,
          endAt: `${url.searchParams.get("date")}T09:30:00-03:00`,
        },
      ],
    });
  }
  if (path.includes("/quote"))
    return json({ doctorId: doctor.id, startAt: start, ...price });
  if (path === "/api/turnos/appointments" && req.method() === "POST") {
    const body = req.postDataJSON();
    assert.equal(body.doctorId, doctor.id);
    assert.equal(body.patientId, expectedTarget);
    appointment = {
      ...appointment,
      patientId: body.patientId,
      startAt: body.startAt,
      endAt: new Date(Date.parse(body.startAt) + 1800000).toISOString(),
      status: "booked",
    };
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
  if (path.endsWith("/reschedule")) {
    if (rejectRescheduleOnce) {
      rejectRescheduleOnce = false;
      return json(
        { error: { code: "SLOT_UNAVAILABLE", message: "Horario ocupado" } },
        409,
      );
    }
    seenReschedule = true;
    appointment = {
      ...appointment,
      id: "smoke-moved",
      startAt: req.postDataJSON().startAt,
      version: 1,
    };
    return json(appointment, 201);
  }
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
  if (path === "/api/pacientes/patients" && req.method() === "POST") {
    patientCreated = true;
    return json({ ...req.postDataJSON(), patientId: "staff-new" }, 201);
  }
  if (path.endsWith("/responsible")) {
    linkSaved = true;
    return json({
      responsiblePatientId: req.postDataJSON().responsiblePatientId,
    });
  }
  if (path === "/api/pacientes/patients")
    return json({ patients: [patient(), child()], page: 1, nextPage: 0 });
  if (path === "/api/pacientes/patients/pat-new" && req.method() === "PUT") {
    profileEdited = true;
    return json({ ...patient(), ...req.postDataJSON() });
  }
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
    .getByText("Cuenta pendiente de habilitación", { exact: true })
    .waitFor();
  enabled = true;
  await page
    .getByRole("button", { name: "Actualizar estado", exact: true })
    .click();
  await page.getByLabel("Atención para", { exact: true }).waitFor();
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
  await page.getByRole("button", { name: "Reprogramar", exact: true }).click();
  await page.locator("label.slot").click();
  await page
    .getByRole("button", { name: "Confirmar reprogramación", exact: true })
    .click();
  await page
    .getByText(
      "No se aplicó el cambio. Tu turno original continúa vigente; elegí otro horario.",
      { exact: true },
    )
    .waitFor();
  await page.locator("label.slot").click();
  await page
    .getByRole("button", { name: "Confirmar reprogramación", exact: true })
    .click();

  await page
    .getByRole("button", { name: "Confirmar reprogramación", exact: true })
    .waitFor({ state: "hidden" });
  assert.ok(seenReschedule);
  checks.push("patient reschedule submits replacement endpoint");
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
  await page.getByRole("link", { name: "Avisos", exact: true }).click();
  await page.getByRole("button", { name: "Marcar leído", exact: true }).click();
  await page.getByText("Leído", { exact: true }).waitFor();
  assert.ok(noticeRead);
  await page
    .getByLabel("Atención para", { exact: true })
    .selectOption("pat-child");
  expectedTarget = "pat-child";
  booked = false;
  await page.getByRole("link", { name: "Buscar médico", exact: true }).click();
  await page
    .getByRole("button", { name: "Ver disponibilidad de Laura Gómez" })
    .click();
  await page.locator("label.slot").click();
  await page
    .getByRole("button", { name: "Confirmar turno", exact: true })
    .click();
  await page
    .getByRole("heading", { name: "Turno confirmado", exact: true })
    .waitFor();
  assert.equal(appointment.patientId, "pat-child");
  checks.push(
    "verified guardian selects dependent and books with child ID; reads notices",
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("link", { name: "Mis turnos", exact: true }).click();
  await page
    .getByRole("heading", { name: "Mis turnos", exact: true })
    .waitFor();
  assert.ok(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  );
  checks.push("390 px mobile appointments layout stays inside viewport");
  await page.setViewportSize({ width: 1280, height: 800 });
  await logout();
  expectedTarget = "pat-new";
  booked = true;
  appointment = { ...appointment, patientId: "pat-new", status: "booked" };

  await login("doctor");
  assert.ok(
    await page
      .getByRole("button", { name: "Registrar atención", exact: true })
      .isDisabled(),
  );
  await page
    .getByRole("button", { name: "Consultar historia clínica", exact: true })
    .click();
  await page
    .getByRole("heading", { name: "Historia clínica", exact: true })
    .waitFor();
  await page
    .getByRole("button", { name: "Ocultar historia", exact: true })
    .click();
  appointment = {
    ...appointment,
    startAt: new Date(Date.now() - 3600000).toISOString(),
  };
  await page.getByRole("button", { name: "Actualizar", exact: true }).click();
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
  checks.push("doctor future note disabled; past encounter permits note");
  await page
    .getByText("Configurar horarios y bloquear agenda", { exact: true })
    .click();
  await page.getByLabel("Duración de nuevas reservas (minutos)").fill("45");
  await page
    .getByRole("button", { name: "Guardar horarios", exact: true })
    .click();
  await page
    .getByText(
      "Agenda actualizada. Las reservas existentes conservan su duración.",
      { exact: true },
    )
    .waitFor();
  assert.ok(scheduleSaved);
  const blockStart = start.slice(0, 16),
    blockEnd = end.slice(0, 16);
  await page
    .getByLabel("Inicio del bloqueo (hora de Argentina)")
    .fill(blockStart);
  await page.getByLabel("Fin del bloqueo (hora de Argentina)").fill(blockEnd);
  await page
    .getByLabel("Motivo que recibirá el paciente")
    .fill("Ausencia de prueba");
  await page
    .getByRole("button", {
      name: "Bloquear y cancelar turnos afectados",
      exact: true,
    })
    .click();
  await page
    .getByRole("button", { name: "Levantar bloqueo", exact: true })
    .click();
  await page.getByText("No hay bloqueos próximos.", { exact: true }).waitFor();
  checks.push("doctor saves schedule, blocks and releases period");
  await logout();
  enabled = false;
  await login("reception");
  await page
    .getByRole("button", { name: "Verificar y habilitar", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Inhabilitar", exact: true })
    .first()
    .waitFor();
  assert.equal(enabled, true);
  checks.push("reception login -> patient verification screen");
  await page
    .getByRole("button", { name: "Editar datos", exact: true })
    .first()
    .click();
  await page
    .getByLabel("Nombre y apellido", { exact: true })
    .last()
    .fill("Nombre corregido");
  await page
    .getByRole("button", { name: "Guardar paciente", exact: true })
    .last()
    .click();
  await page
    .getByRole("heading", {
      name: "Editar datos de Paciente nuevo",
      exact: true,
    })
    .waitFor({ state: "hidden" });
  assert.ok(profileEdited);
  await page
    .getByLabel("Profesional de la agenda", { exact: true })
    .selectOption(doctor.id);
  await page
    .getByLabel("Paciente para asignar turno", { exact: true })
    .selectOption("pat-new");
  await page
    .getByRole("button", {
      name: "Asignar turno al paciente seleccionado",
      exact: true,
    })
    .click();
  await page.locator("label.slot").click();
  await page
    .getByRole("button", { name: "Confirmar turno", exact: true })
    .click();
  await page
    .getByRole("heading", { name: "Turno confirmado", exact: true })
    .waitFor();
  checks.push("reception edits patient and assigns reservation");
  await page.getByText("Registrar un paciente", { exact: true }).click();
  await page
    .getByLabel("Nombre y apellido", { exact: true })
    .fill("Ficha nueva");
  await page.getByLabel("DNI del paciente", { exact: true }).fill("33445566");
  await page
    .getByLabel("Fecha de nacimiento del paciente", { exact: true })
    .fill("1990-01-01");
  await page
    .getByRole("button", { name: "Guardar paciente", exact: true })
    .click();
  await page.getByText("Paciente registrado.", { exact: true }).waitFor();
  assert.ok(patientCreated);
  await page.getByLabel("Menor", { exact: true }).selectOption("pat-child");
  await page
    .getByLabel("Adulto responsable verificado", { exact: true })
    .selectOption("pat-new");
  await page
    .getByRole("button", { name: "Registrar vínculo verificado", exact: true })
    .click();
  await page
    .getByText("Vínculo verificado actualizado.", { exact: true })
    .waitFor();
  assert.ok(linkSaved);
  checks.push("reception registers patient and verifies guardian link");
  await logout();
  await login("admin");
  await page.getByLabel("Identificador del profesional").fill("doc-001");
  await page
    .getByRole("button", { name: "Consultar perfil", exact: true })
    .click();
  await page.getByLabel("Nombre", { exact: true }).waitFor();
  checks.push("admin login -> doctor profile administration");
  await logout();
  catalog = [
    doctor,
    ...Array.from({ length: 8 }, (_, i) => ({
      ...doctor,
      id: `doc-00${i + 2}`,
      fullName: `Profesional ${i + 2}`,
    })),
    { ...doctor, id: "doc-010", fullName: "Zulu prueba" },
  ];
  farSearch = true;
  await login("patient");
  await page
    .getByRole("button", { name: "Ver disponibilidad de Zulu prueba" })
    .waitFor();
  assert.match(
    await page.locator(".board__table tbody tr").first().innerText(),
    /Zulu prueba/,
  );
  await page.getByLabel("Ordenar por", { exact: true }).selectOption("name");
  await page
    .getByRole("button", { name: "Ver disponibilidad de Laura Gómez" })
    .click();
  const farDate = new Intl.DateTimeFormat("en-CA", {
    timeZone: "America/Argentina/Buenos_Aires",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date(Date.now() + 20 * 86400000));
  assert.equal(
    await page.getByLabel("Fecha de la consulta", { exact: true }).inputValue(),
    farDate,
  );
  checks.push(
    "global availability ordering across 10 doctors and selection beyond 10 days",
  );
  assert.deepEqual(errors, []);
  for (const check of checks) console.log("OK", check);
  console.log(
    `${checks.length} browser flows passed; HTTP fixtures, not live infrastructure.`,
  );
} finally {
  await browser.close();
  await new Promise((resolve) => server.close(resolve));
}
