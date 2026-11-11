import { usePatientTarget } from "../patients/PatientContext";
import { useEffect, useState } from "react";
import { RefreshCw, X } from "lucide-react";
import { turnos } from "../../api/endpoints";
import { ApiError } from "../../api/http";
import type {
  Appointment,
  Availability,
  Doctor,
  Quote,
  Slot,
} from "../../api/types";
import {
  dateAtNoon,
  formatDay,
  formatLongDay,
  formatTime,
  specialtyName,
  upcomingDays,
} from "../../format";
import type { Session } from "../../session";
import { Notice } from "../../ui/Notice";
import { Pass } from "../../ui/Pass";

interface Props {
  session: Session;
  patientId?: string;
  initialDate?: string;
  reschedule?: Appointment;
  doctor: Doctor;
  onClose: () => void;
  onBooked: (a: Appointment) => void;
}

type Slots =
  | { kind: "loading" }
  | { kind: "ready"; data: Availability }
  | { kind: "error"; message: string; unavailable: boolean };
type QuoteState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "ready"; quote: Quote }
  | { kind: "missing" };

export function BookingPanel({
  session,
  doctor,
  patientId,
  initialDate,
  reschedule,
  onClose,
  onBooked,
}: Props) {
  const target = usePatientTarget();
  const [clock, setClock] = useState(Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setClock(Date.now()), 60000);
    return () => window.clearInterval(timer);
  }, []);
  const days = upcomingDays(60, new Date(clock));
  const [day, setDay] = useState(
    initialDate && initialDate >= days[0] && initialDate <= days[59]
      ? initialDate
      : days[0],
  );
  useEffect(() => {
    if (day < days[0] || day > days[59]) setDay(days[0]);
  }, [days[0], day]);
  const [slots, setSlots] = useState<Slots>({ kind: "loading" });
  const [reload, setReload] = useState(0);
  const [slot, setSlot] = useState<Slot | null>(null);
  const [quote, setQuote] = useState<QuoteState>({ kind: "idle" });
  const [booking, setBooking] = useState(false);
  const [problem, setProblem] = useState<{
    tone: "alert" | "error";
    title: string;
    text: string;
  } | null>(null);
  const [issued, setIssued] = useState<Appointment | null>(null);

  useEffect(() => {
    let alive = true;
    setSlots({ kind: "loading" });
    setSlot(null);
    turnos
      .availability(doctor.doctorId, day)
      .then((data) => alive && setSlots({ kind: "ready", data }))
      .catch((err: unknown) => {
        if (!alive) return;
        const e = err instanceof ApiError ? err : null;
        setSlots({
          kind: "error",
          message: e?.message ?? "No pudimos traer los horarios.",
          unavailable: e?.unavailable ?? false,
        });
      });
    return () => {
      alive = false;
    };
  }, [doctor.doctorId, day, reload]);

  useEffect(() => {
    if (!slot) {
      setQuote({ kind: "idle" });
      return;
    }
    let alive = true;
    setQuote({ kind: "loading" });
    turnos
      .quote(doctor.doctorId, slot.startAt)
      .then((q) => alive && setQuote({ kind: "ready", quote: q }))
      .catch(() => alive && setQuote({ kind: "missing" }));
    return () => {
      alive = false;
    };
  }, [doctor.doctorId, slot]);

  async function confirm() {
    if (!slot) return;
    setBooking(true);
    setProblem(null);
    try {
      const appointment = reschedule
        ? await turnos.reschedule(reschedule.id, slot.startAt)
        : await turnos.book(
            patientId ?? session.subject,
            doctor.doctorId,
            slot.startAt,
          );
      setIssued(appointment);
      onBooked(appointment);
    } catch (err) {
      setProblem(bookingProblem(err));
      if (err instanceof ApiError && err.status === 409)
        setReload((n) => n + 1);
    } finally {
      setBooking(false);
    }
  }

  const specialties = doctor.specialties.map(specialtyName);

  if (issued) {
    return (
      <section className="panel" aria-labelledby="issued-title">
        <header className="panel__head">
          <h2 id="issued-title" className="panel__title">
            {reschedule ? "Turno reprogramado" : "Turno confirmado"}
          </h2>
          <button
            type="button"
            className="icon-btn"
            onClick={onClose}
            aria-label="Cerrar"
          >
            <X size={18} aria-hidden="true" />
          </button>
        </header>
        <Pass
          label="Pase del turno confirmado"
          appointmentId={issued.id}
          doctorName={doctor.fullName}
          specialties={specialties}
          startAt={issued.startAt}
          endAt={issued.endAt}
          status={issued.status}
          price={
            issued.price ?? (quote.kind === "ready" ? quote.quote : undefined)
          }
        >
          <a
            className="btn btn--primary btn--block"
            href={
              session.role === "patient" ? "#/appointments" : "#/operations"
            }
          >
            {session.role === "patient" ? "Ver en mis turnos" : "Ver en agenda"}
          </a>
        </Pass>
      </section>
    );
  }

  return (
    <section className="panel" aria-labelledby="booking-title">
      <header className="panel__head">
        <div>
          <h2 id="booking-title" className="panel__title">
            {doctor.fullName}
          </h2>
          <p className="panel__sub">{specialties.join(" · ")}</p>
        </div>
        <button
          type="button"
          className="icon-btn"
          onClick={onClose}
          aria-label="Cerrar reserva"
        >
          <X size={18} aria-hidden="true" />
        </button>
      </header>

      {reschedule ? (
        <Notice tone="alert" title="Reprogramar turno">
          El turno original se conserva si el nuevo horario no puede
          confirmarse.
        </Notice>
      ) : null}
      <div className="field">
        <label htmlFor={`booking-date-${doctor.doctorId}`}>
          Fecha de la consulta
        </label>
        <input
          id={`booking-date-${doctor.doctorId}`}
          type="date"
          min={days[0]}
          max={days[59]}
          value={day}
          onChange={(e) => {
            if (e.target.value >= days[0] && e.target.value <= days[59])
              setDay(e.target.value);
          }}
        />
      </div>
      <fieldset className="days">
        <legend className="label">Día</legend>
        <div className="days__strip">
          {days.slice(0, 10).map((d) => (
            <label key={d} className="day">
              <input
                type="radio"
                name="day"
                value={d}
                checked={d === day}
                onChange={() => setDay(d)}
              />
              <span className="day__box">{formatDay(dateAtNoon(d))}</span>
            </label>
          ))}
        </div>
      </fieldset>

      <div className="slots">
        <div className="slots__head">
          <span className="label">
            Horarios · {formatLongDay(dateAtNoon(day))}
          </span>
          <button
            type="button"
            className="icon-btn icon-btn--small"
            onClick={() => setReload((n) => n + 1)}
            aria-label="Actualizar horarios"
          >
            <RefreshCw size={14} aria-hidden="true" />
          </button>
        </div>
        {slots.kind === "loading" ? (
          <p className="slots__msg" aria-busy="true">
            Buscando horarios libres…
          </p>
        ) : null}
        {slots.kind === "error" ? (
          <Notice
            tone={slots.unavailable ? "alert" : "error"}
            title="No pudimos traer los horarios"
          >
            {slots.unavailable
              ? "El servicio de turnos no responde. Probá de nuevo en unos segundos."
              : slots.message}
          </Notice>
        ) : null}
        {slots.kind === "ready" && slots.data.slots.length === 0 ? (
          <p className="slots__msg">
            No quedan horarios libres este día. Elegí otro día.
          </p>
        ) : null}
        {slots.kind === "ready" && slots.data.slots.length > 0 ? (
          <fieldset className="slots__grid">
            <legend className="visually-hidden">Horario</legend>
            {slots.data.slots.map((s) => (
              <label
                key={s.startAt}
                className="slot"
                title={tooSoon(s) ? "Requiere 24 h de anticipación" : undefined}
              >
                <input
                  type="radio"
                  name="slot"
                  value={s.startAt}
                  disabled={tooSoon(s)}
                  checked={slot?.startAt === s.startAt}
                  onChange={() => {
                    setSlot(s);
                    setProblem(null);
                  }}
                />
                <span className="slot__box">{formatTime(s.startAt)}</span>
              </label>
            ))}
          </fieldset>
        ) : null}
        {slots.kind === "ready" && slots.data.slots.some(tooSoon) ? (
          <p className="slots__msg">
            Los horarios tachados empiezan en menos de 24 h y no se pueden
            reservar.
          </p>
        ) : null}
      </div>

      {problem ? (
        <Notice tone={problem.tone} title={problem.title}>
          {problem.text}
        </Notice>
      ) : null}

      {slot ? (
        <Pass
          label="Pase provisorio del turno elegido"
          doctorName={doctor.fullName}
          specialties={specialties}
          startAt={slot.startAt}
          endAt={slot.endAt}
          price={quote.kind === "ready" ? quote.quote : undefined}
          priceLoading={quote.kind === "loading"}
        >
          <button
            type="button"
            className="btn btn--primary btn--block"
            onClick={confirm}
            disabled={booking || target?.profile?.enabled === false}
          >
            {booking
              ? "Confirmando…"
              : reschedule
                ? "Confirmar reprogramación"
                : "Confirmar turno"}
          </button>
        </Pass>
      ) : (
        <p className="panel__hint">
          Elegí un horario para ver el pase con el total de la consulta.
        </p>
      )}
    </section>
  );
}

const NOTICE_MS = 24 * 60 * 60 * 1000;

/** Bookings need 24 h notice; Turnos enforces it, the UI just says so up front. */
function tooSoon(s: Slot): boolean {
  return new Date(s.startAt).getTime() - Date.now() < NOTICE_MS;
}

function bookingProblem(err: unknown): {
  tone: "alert" | "error";
  title: string;
  text: string;
} {
  if (!(err instanceof ApiError))
    return {
      tone: "error",
      title: "No pudimos reservar",
      text: "Intentá de nuevo.",
    };
  if (err.status === 409)
    return {
      tone: "alert",
      title: "Ese horario se acaba de ocupar",
      text: "Otra persona lo reservó antes. Actualizamos la lista: elegí otro horario.",
    };
  if (err.status === 403)
    return {
      tone: "alert",
      title: "Tu cuenta todavía no está habilitada",
      text: "Recepción tiene que verificar tus datos antes de que puedas reservar.",
    };
  if (err.status === 504)
    return {
      tone: "alert",
      title: "No sabemos si el turno quedó reservado",
      text: "El servicio tardó demasiado. Revisá «Mis turnos» antes de volver a intentar.",
    };
  if (err.unavailable)
    return {
      tone: "alert",
      title: "El servicio de turnos no responde",
      text: "Probá de nuevo en unos segundos.",
    };
  if (err.status === 429)
    return {
      tone: "alert",
      title: "Demasiadas solicitudes",
      text: "Esperá unos segundos y volvé a confirmar.",
    };
  return { tone: "error", title: "No pudimos reservar", text: err.message };
}
