import type { Money } from "./api/types";

// Every time in the portal is shown in the clinic's zone, whatever the
// visitor's machine says.
export const CLINIC_TZ = "America/Argentina/Cordoba";

const dayFmt = new Intl.DateTimeFormat("es-AR", {
  timeZone: CLINIC_TZ,
  weekday: "short",
  day: "2-digit",
  month: "short",
});
const longDayFmt = new Intl.DateTimeFormat("es-AR", {
  timeZone: CLINIC_TZ,
  weekday: "long",
  day: "numeric",
  month: "long",
});
const timeFmt = new Intl.DateTimeFormat("es-AR", {
  timeZone: CLINIC_TZ,
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});
const isoDayFmt = new Intl.DateTimeFormat("en-CA", {
  timeZone: CLINIC_TZ,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
});

export const formatDay = (iso: string) =>
  dayFmt.format(new Date(iso)).replace(".", "").toUpperCase();
export const formatLongDay = (iso: string) => longDayFmt.format(new Date(iso));
export const formatTime = (iso: string) => timeFmt.format(new Date(iso));

/** yyyy-mm-dd of an instant in the clinic's zone. */
export const clinicDate = (d: Date) => isoDayFmt.format(d);

/** Next `count` weekdays starting tomorrow (bookings need 24 h notice). */
export function upcomingWeekdays(count: number, from = new Date()): string[] {
  const days: string[] = [];
  const cursor = new Date(from.getTime() + 24 * 60 * 60 * 1000);
  while (days.length < count) {
    const weekday = new Intl.DateTimeFormat("en-US", {
      timeZone: CLINIC_TZ,
      weekday: "short",
    }).format(cursor);
    if (weekday !== "Sat" && weekday !== "Sun") days.push(clinicDate(cursor));
    cursor.setTime(cursor.getTime() + 24 * 60 * 60 * 1000);
  }
  return days;
}

/** Noon of a clinic date, safe to format without crossing midnight. */
export const dateAtNoon = (day: string) => `${day}T12:00:00-03:00`;

const arsFmt = new Intl.NumberFormat("es-AR", {
  style: "currency",
  currency: "ARS",
  minimumFractionDigits: 2,
});
const kwhFmt = new Intl.NumberFormat("es-AR", { maximumFractionDigits: 3 });

/** Formats a decimal string without float arithmetic when it is already exact. */
export function formatArs(value: Money | null | undefined): string {
  if (value === null || value === undefined || value === "") return "—";
  const n = typeof value === "number" ? value : Number(value);
  return Number.isFinite(n) ? arsFmt.format(n) : String(value);
}

export function formatKwh(value: Money | null | undefined): string {
  if (value === null || value === undefined || value === "") return "—";
  const n = typeof value === "number" ? value : Number(value);
  return Number.isFinite(n) ? `${kwhFmt.format(n)} kWh` : `${value} kWh`;
}

/** Short, stable folio for an appointment id, as printed on the pass. */
export function folio(id: string): string {
  return id
    .replace(/[^a-zA-Z0-9]/g, "")
    .slice(-6)
    .toUpperCase()
    .padStart(6, "0");
}

export const SPECIALTY_LABELS: Record<string, string> = {};
export function specialtyName(code: string): string {
  return SPECIALTY_LABELS[code] ?? code.replace(/[-_]/g, " ");
}

/** All clinic days in the same 60-day horizon as Turnos, including weekends. */
export function upcomingDays(count = 60, from = new Date()): string[] {
  const start = from.getTime() + 24 * 60 * 60 * 1000;
  return Array.from({ length: count }, (_, i) =>
    clinicDate(new Date(start + i * 24 * 60 * 60 * 1000)),
  );
}
export function isMinor(birthDate: string, now = new Date()): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(birthDate)) return false;
  const [y, m, d] = birthDate.split("-").map(Number);
  const adult = new Date(Date.UTC(y + 18, m - 1, d)).toISOString().slice(0, 10);
  return adult > clinicDate(now);
}
