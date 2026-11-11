import { request, ApiError } from "./http";
import type {
  Appointment,
  AppointmentList,
  Availability,
  ClinicalHistory,
  ClinicalNote,
  Doctor,
  NextAvailable,
  LoginResponse,
  Profile,
  Quote,
  RegisterRequest,
  SearchQuery,
  SearchResult,
  Specialty,
  PatientProfile,
  DoctorSchedule,
  AgendaBlock,
  PortalNotice,
} from "./types";

const enc = encodeURIComponent;
// HTTP profiles use id; normalize both list and detail at the boundary.
type WireDoctor = Omit<Doctor, "doctorId"> & { id: string };
function doctorFromWire(d: WireDoctor): Doctor {
  if (!d || typeof d.id !== "string" || !d.id)
    throw new ApiError(
      502,
      "BAD_RESPONSE",
      "El profesional no tiene un identificador válido.",
    );
  return { ...d, doctorId: d.id };
}

export const pacientes = {
  dependents: () =>
    request<{ patients: PatientProfile[] }>("GET", "/api/pacientes/dependents"),
  patients: (page = 1, q = "") =>
    request<{ patients: PatientProfile[]; nextPage: number }>(
      "GET",
      `/api/pacientes/patients?page=${page}&q=${enc(q)}`,
    ),
  create: (
    body: Omit<PatientProfile, "patientId" | "responsiblePatientId"> & {
      password?: string;
    },
  ) => request<PatientProfile>("POST", "/api/pacientes/patients", body),
  update: (
    id: string,
    body: Pick<
      PatientProfile,
      "fullName" | "email" | "documentNumber" | "birthDate"
    >,
  ) =>
    request<PatientProfile>("PUT", `/api/pacientes/patients/${enc(id)}`, body),
  login: (email: string, password: string) =>
    request<LoginResponse>("POST", "/api/pacientes/auth/login", {
      email,
      password,
    }),
  register: (body: RegisterRequest) =>
    request<{ patientId: string }>(
      "POST",
      "/api/pacientes/auth/register",
      body,
    ),
  me: () => request<Profile>("GET", "/api/pacientes/me"),
  contact: (patientId: string) =>
    request<{ patientId: string; fullName: string }>(
      "GET",
      `/api/pacientes/patients/${enc(patientId)}/contact`,
    ),
};

export const medicos = {
  catalog: async (q: SearchQuery): Promise<SearchResult> => {
    let items: Doctor[] = [];
    let page = 1;
    let total = 0;
    do {
      const result = await medicos.search({
        ...q,
        sort: "name",
        page,
        size: 50,
      });
      total = result.total;
      if (total > 1000)
        throw new ApiError(
          422,
          "REFINE_SEARCH",
          "Hay más de 1000 profesionales. Refiná el nombre o la especialidad.",
        );
      items = items.concat(result.items);
      if (!result.items.length) break;
      page++;
    } while (items.length < total);
    return { items, total, page: 1, size: items.length };
  },
  search: (q: SearchQuery) => {
    const params = new URLSearchParams({
      sort: q.sort,
      page: String(q.page),
      size: String(q.size),
    });
    if (q.q.trim()) params.set("q", q.q.trim());
    if (q.specialty) params.set("specialty", q.specialty);
    if (q.acceptsMinors) params.set("acceptsMinors", "true");
    return request<Omit<SearchResult, "items"> & { items: WireDoctor[] }>(
      "GET",
      `/api/medicos/search?${params}`,
    ).then((r) => ({ ...r, items: r.items.map(doctorFromWire) }));
  },
  specialties: () =>
    request<{ specialties: Specialty[] }>("GET", "/api/medicos/specialties"),
  doctor: (doctorId: string) =>
    request<WireDoctor>("GET", `/api/medicos/doctors/${enc(doctorId)}`).then(
      doctorFromWire,
    ),
};

export const turnos = {
  page: (page = 1, filters: { patientId?: string; doctorId?: string } = {}) =>
    request<AppointmentList>(
      "GET",
      `/api/turnos/appointments?${new URLSearchParams({ page: String(page), ...(filters.patientId ? { patientId: filters.patientId } : {}), ...(filters.doctorId ? { doctorId: filters.doctorId } : {}) })}`,
    ),
  reschedule: (id: string, startAt: string) =>
    request<Appointment>(
      "POST",
      `/api/turnos/appointments/${enc(id)}/reschedule`,
      { startAt },
    ),
  schedule: (id: string) =>
    request<DoctorSchedule>("GET", `/api/turnos/doctors/${enc(id)}/schedule`),
  setSchedule: (id: string, body: DoctorSchedule) =>
    request<DoctorSchedule>(
      "PUT",
      `/api/turnos/doctors/${enc(id)}/schedule`,
      body,
    ),
  blocks: (id: string) =>
    request<{ blocks: AgendaBlock[] }>(
      "GET",
      `/api/turnos/doctors/${enc(id)}/blocks`,
    ),
  block: (id: string, startAt: string, endAt: string, reason: string) =>
    request<AgendaBlock>("POST", `/api/turnos/doctors/${enc(id)}/blocks`, {
      startAt,
      endAt,
      reason,
    }),
  releaseBlock: (id: string, block: string) =>
    request(
      "POST",
      `/api/turnos/doctors/${enc(id)}/blocks/${enc(block)}/release`,
      {},
    ),
  availability: (doctorId: string, date: string) =>
    request<Availability>(
      "GET",
      `/api/turnos/doctors/${enc(doctorId)}/availability?date=${enc(date)}`,
    ),
  nextAvailable: (doctorId: string) =>
    request<NextAvailable>(
      "GET",
      `/api/turnos/doctors/${enc(doctorId)}/next-available`,
    ),
  quote: (doctorId: string, startAt: string) =>
    request<Quote>(
      "GET",
      `/api/turnos/doctors/${enc(doctorId)}/quote?startAt=${enc(startAt)}`,
    ),
  book: (patientId: string, doctorId: string, startAt: string) =>
    request<Appointment>("POST", "/api/turnos/appointments", {
      patientId,
      doctorId,
      startAt,
    }),
  list: (patientId?: string) =>
    request<AppointmentList | Appointment[]>(
      "GET",
      `/api/turnos/appointments${patientId ? `?patientId=${enc(patientId)}` : ""}`,
    ).then(normalizeList),
  cancel: (appointmentId: string, reason: string) =>
    request<Appointment>(
      "POST",
      `/api/turnos/appointments/${enc(appointmentId)}/cancel`,
      { reason },
    ),
};

export const notifications = {
  page: (page = 1, patientId?: string) =>
    request<{ notices: PortalNotice[]; nextPage: number }>(
      "GET",
      `/api/notifications/notices?page=${page}${patientId ? `&patientId=${enc(patientId)}` : ""}`,
    ),
  read: (id: string, patientId?: string) =>
    request(
      "POST",
      `/api/notifications/notices/${enc(id)}/read${patientId ? `?patientId=${enc(patientId)}` : ""}`,
      {},
    ),
};

export const historia = {
  patient: (patientId: string) =>
    request<ClinicalHistory>(
      "GET",
      `/api/historia/records/patients/${enc(patientId)}/timeline`,
    ),
  mine: () => request<ClinicalHistory>("GET", "/api/historia/records/me"),
  addNote: (appointmentId: string, text: string) =>
    request<ClinicalNote>(
      "POST",
      `/api/historia/records/${enc(appointmentId)}/notes`,
      { text },
    ),
};

function normalizeList(
  payload: AppointmentList | Appointment[],
): Appointment[] {
  if (Array.isArray(payload)) return payload;
  if (!Array.isArray(payload.appointments))
    throw new ApiError(
      502,
      "BAD_RESPONSE",
      "La lista de turnos tiene un formato inesperado.",
    );
  return payload.appointments;
}
