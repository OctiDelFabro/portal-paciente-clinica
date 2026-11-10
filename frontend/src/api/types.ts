// Contracts of the services behind the gateway, as published in each
// service README. Field names match the JSON exactly.

export type Role = "patient" | "doctor" | "admin" | "reception";

export interface LoginResponse {
  accessToken: string;
  tokenType: "Bearer";
  expiresIn: number;
  patient?: { patientId: string; fullName: string; email: string };
  user?: { userId: string; fullName: string; email: string; role: Role };
}

export interface RegisterRequest {
  email: string;
  password: string;
  fullName: string;
  documentNumber: string;
  birthDate: string;
}

export interface Profile {
  patientId: string;
  fullName: string;
  email: string;
  documentNumber: string;
  birthDate: string;
  enabled: boolean;
  responsiblePatientId?: string | null;
}

export interface Doctor {
  doctorId: string;
  fullName: string;
  specialties: string[];
  acceptsMinors: boolean;
  active: boolean;
  nextAvailableAt?: string;
}

export interface SearchResult {
  items: Doctor[];
  page: number;
  size: number;
  total: number;
}

export interface Specialty {
  code: string;
  name: string;
}

export type SortOrder = "name" | "nextAvailable";

export interface SearchQuery {
  q: string;
  specialty: string;
  acceptsMinors: boolean;
  sort: SortOrder;
  page: number;
  size: number;
}

export interface Slot {
  startAt: string;
  endAt: string;
}

export interface Availability {
  doctorId: string;
  date: string;
  durationMinutes: number;
  slots: Slot[];
}

/** Money travels as a decimal string or number; never do arithmetic on it in the browser. */
export type Money = string | number;

export interface EnergyCost {
  status: "estimated" | "pending";
  kWh?: Money | null;
  costArs?: Money | null;
  algorithmVersion?: string | null;
}

export interface Price {
  basePriceArs: Money;
  energy: EnergyCost;
  totalArs: Money;
}

export interface Quote extends Price {
  doctorId: string;
  startAt: string;
}

export type AppointmentStatus = "booked" | "cancelled" | string;

export interface Appointment {
  id: string;
  patientId: string;
  doctorId: string;
  startAt: string;
  endAt: string;
  status: AppointmentStatus;
  version: number;
  cancelReason?: string;
  price?: Price;
}

export interface AppointmentList {
  appointments: Appointment[];
  page?: number;
  nextPage?: number;
}

export interface ClinicalNote {
  noteId?: string;
  text: string;
  doctorId?: string;
  createdAt: string;
}

export interface ClinicalRecord {
  appointmentId: string;
  patientId: string;
  doctorId: string;
  startAt: string;
  status: "scheduled" | "cancelled";
  cancelReason?: string;
  notes: ClinicalNote[];
}

export interface ClinicalHistory {
  patientId: string;
  records: ClinicalRecord[];
}
