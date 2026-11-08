import { request } from './http';
import type {
  Appointment,
  AppointmentList,
  Availability,
  ClinicalHistory,
  ClinicalNote,
  Doctor,
  LoginResponse,
  Profile,
  Quote,
  RegisterRequest,
  SearchQuery,
  SearchResult,
  Specialty,
} from './types';

const enc = encodeURIComponent;

export const pacientes = {
  login: (email: string, password: string) =>
    request<LoginResponse>('POST', '/api/pacientes/auth/login', { email, password }),
  register: (body: RegisterRequest) =>
    request<{ patientId: string }>('POST', '/api/pacientes/auth/register', body),
  me: () => request<Profile>('GET', '/api/pacientes/me'),
  contact: (patientId: string) =>
    request<{ patientId: string; fullName: string }>('GET', `/api/pacientes/patients/${enc(patientId)}/contact`),
};

export const medicos = {
  search: (q: SearchQuery) => {
    const params = new URLSearchParams({ sort: q.sort, page: String(q.page), size: String(q.size) });
    if (q.q.trim()) params.set('q', q.q.trim());
    if (q.specialty) params.set('specialty', q.specialty);
    if (q.acceptsMinors) params.set('acceptsMinors', 'true');
    return request<SearchResult>('GET', `/api/medicos/search?${params}`);
  },
  specialties: () => request<{ specialties: Specialty[] }>('GET', '/api/medicos/specialties'),
  doctor: (doctorId: string) => request<Doctor>('GET', `/api/medicos/doctors/${enc(doctorId)}`),
};

export const turnos = {
  availability: (doctorId: string, date: string) =>
    request<Availability>('GET', `/api/turnos/doctors/${enc(doctorId)}/availability?date=${enc(date)}`),
  quote: (doctorId: string, startAt: string) =>
    request<Quote>('GET', `/api/turnos/doctors/${enc(doctorId)}/quote?startAt=${enc(startAt)}`),
  book: (patientId: string, doctorId: string, startAt: string) =>
    request<Appointment>('POST', '/api/turnos/appointments', { patientId, doctorId, startAt }),
  list: () => request<AppointmentList | Appointment[]>('GET', '/api/turnos/appointments').then(normalizeList),
  cancel: (appointmentId: string, reason: string) =>
    request<Appointment>('POST', `/api/turnos/appointments/${enc(appointmentId)}/cancel`, { reason }),
};

export const historia = {
  mine: () => request<ClinicalHistory>('GET', '/api/historia/records/me'),
  addNote: (appointmentId: string, text: string) =>
    request<ClinicalNote>('POST', `/api/historia/records/${enc(appointmentId)}/notes`, { text }),
};

function normalizeList(payload: AppointmentList | Appointment[]): Appointment[] {
  return Array.isArray(payload) ? payload : payload.items ?? [];
}
