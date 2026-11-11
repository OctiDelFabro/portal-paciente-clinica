import { usePatientTarget } from "../patients/PatientContext";
import { usePatientNames } from "../patients/usePatientNames";
import { BookingPanel } from "../booking/BookingPanel";
import type { Session } from "../../session";
import { useLayoutEffect, useRef, useState, type FormEvent } from "react";
import { ChevronRight, RefreshCw } from "lucide-react";
import { turnos } from "../../api/endpoints";
import { ApiError } from "../../api/http";
import type { Appointment } from "../../api/types";
import { folio, formatArs, formatDay, formatTime } from "../../format";
import { Notice } from "../../ui/Notice";
import { ChangedTag, EnergyTag, StatusTag } from "../../ui/StatusTag";
import { useDoctors } from "../doctors";
import { useAppointments } from "./useAppointments";

const DAY_MS = 24 * 60 * 60 * 1000;

export function AppointmentsBoard({
  session,
  filters = {},
  staff = false,
}: {
  session: Session;
  filters?: { patientId?: string; doctorId?: string };
  staff?: boolean;
}) {
  const target = usePatientTarget();
  const [rescheduling, setRescheduling] = useState<Appointment | null>(null);
  const {
    items,
    state,
    changed,
    updatedAt,
    refresh,
    acknowledge,
    hasMore,
    refreshing,
    loadMore,
  } = useAppointments({
    ...filters,
    patientId: filters.patientId ?? target?.patientId,
  });
  const patients = usePatientNames(staff ? items.map((a) => a.patientId) : []);
  const doctors = useDoctors(items.map((a) => a.doctorId));
  const [cancelling, setCancelling] = useState<string | null>(null);
  const [flash, setFlash] = useState<{
    tone: "alert" | "error";
    title: string;
    text: string;
  } | null>(null);
  const rows = useFlip(items.map((a) => a.id));

  const rescheduleDoctor = rescheduling ? doctors[rescheduling.doctorId] : null;
  const now = Date.now();
  const upcoming = items.filter((a) => new Date(a.startAt).getTime() >= now);
  const past = items.filter((a) => new Date(a.startAt).getTime() < now);

  return (
    <section className="single" aria-labelledby="appts-title">
      <header className="screen-head screen-head--row">
        <div>
          <h1 id="appts-title" className="screen-title">
            {staff ? "Agenda y turnos" : "Mis turnos"}
          </h1>
          <p className="screen-lede">
            Ordenados por fecha. Si algo cambia, la fila queda marcada hasta que
            la veas.
          </p>
        </div>
        <div className="refresh">
          <span className="refresh__stamp" aria-live="polite">
            {updatedAt
              ? `Actualizado ${formatTime(updatedAt.toISOString())}`
              : "Actualizando…"}
          </span>
          <button
            type="button"
            className="btn btn--ghost"
            onClick={() => void refresh()}
          >
            <RefreshCw size={16} aria-hidden="true" /> Actualizar
          </button>
        </div>
      </header>

      {rescheduling && rescheduleDoctor ? (
        <div className="reschedule-panel">
          <BookingPanel
            key={rescheduling.id}
            session={session}
            doctor={rescheduleDoctor}
            patientId={rescheduling.patientId}
            reschedule={rescheduling}
            onClose={() => setRescheduling(null)}
            onBooked={() => {
              setRescheduling(null);
              void refresh();
            }}
          />
        </div>
      ) : null}
      {state.kind === "error" ? (
        <Notice
          tone={state.unavailable ? "alert" : "error"}
          title="No pudimos actualizar tus turnos"
        >
          {state.unavailable
            ? "El servicio de turnos no responde. Mostramos la última información disponible."
            : state.message}
        </Notice>
      ) : null}
      {flash ? (
        <Notice tone={flash.tone} title={flash.title}>
          {flash.text}
        </Notice>
      ) : null}

      <div className="board board--live" aria-busy={state.kind === "loading"}>
        <table className="board__table">
          <caption className="visually-hidden">
            Turnos próximos, ordenados por fecha
          </caption>
          <thead>
            <tr>
              <th scope="col">Folio</th>
              <th scope="col">Fecha</th>
              <th scope="col">Hora</th>
              <th scope="col">Profesional</th>
              <th scope="col">Estado</th>
              <th scope="col">Energía</th>
              <th scope="col" className="num">
                Total
              </th>
              <th scope="col">
                <span className="visually-hidden">Acciones</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {state.kind === "ready" && upcoming.length === 0 ? (
              <tr>
                <td colSpan={8} className="board__empty">
                  No tenés turnos próximos.{" "}
                  <a href="#/search">Buscá un profesional</a> para reservar.
                </td>
              </tr>
            ) : null}
            {upcoming.map((a) => {
              const isChanged = changed.has(a.id);
              const cancellable =
                a.status === "booked" &&
                new Date(a.startAt).getTime() - now >= DAY_MS;
              return (
                <Row
                  key={a.id}
                  rowRef={rows(a.id)}
                  appointment={a}
                  doctorName={doctors[a.doctorId]?.fullName ?? a.doctorId}
                  patientName={
                    staff ? (patients[a.patientId] ?? a.patientId) : undefined
                  }
                  onReschedule={() => setRescheduling(a)}
                  changed={isChanged}
                  onSeen={() => acknowledge(a.id)}
                  cancellable={cancellable}
                  cancelling={cancelling === a.id}
                  onCancelStart={() => {
                    setFlash(null);
                    setCancelling(a.id);
                  }}
                  onCancelDone={(result) => {
                    setCancelling(null);
                    if (result) setFlash(result);
                    void refresh();
                  }}
                />
              );
            })}
          </tbody>
        </table>
      </div>

      {hasMore ? (
        <button
          className="btn btn--secondary"
          disabled={refreshing}
          onClick={loadMore}
        >
          Cargar más turnos
        </button>
      ) : null}
      {past.length > 0 ? (
        <details className="past">
          <summary>
            <ChevronRight
              size={16}
              aria-hidden="true"
              className="past__chevron"
            />
            Turnos pasados ({past.length})
          </summary>
          <ul className="past__list">
            {past
              .slice()
              .reverse()
              .map((a) => (
                <li key={a.id}>
                  <span className="num">N° {folio(a.id)}</span>
                  <span className="num">
                    {formatDay(a.startAt)} {formatTime(a.startAt)}
                  </span>
                  <span>{doctors[a.doctorId]?.fullName ?? a.doctorId}</span>
                  <StatusTag status={a.status} />
                </li>
              ))}
          </ul>
        </details>
      ) : null}
    </section>
  );
}

interface RowProps {
  rowRef: (el: HTMLTableRowElement | null) => void;
  appointment: Appointment;
  doctorName: string;
  patientName?: string;
  onReschedule: () => void;
  changed: boolean;
  onSeen: () => void;
  cancellable: boolean;
  cancelling: boolean;
  onCancelStart: () => void;
  onCancelDone: (
    notice: { tone: "alert" | "error"; title: string; text: string } | null,
  ) => void;
}

function Row({
  rowRef,
  appointment: a,
  doctorName,
  patientName,
  onReschedule,
  changed,
  onSeen,
  cancellable,
  cancelling,
  onCancelStart,
  onCancelDone,
}: RowProps) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await turnos.cancel(a.id, reason.trim() || "Cancelado por el paciente");
      onCancelDone({
        tone: "alert",
        title: `Turno N° ${folio(a.id)} cancelado`,
        text: "El horario quedó libre para otra persona.",
      });
    } catch (err) {
      const e = err instanceof ApiError ? err : null;
      onCancelDone({
        tone: e?.status === 422 || e?.status === 409 ? "alert" : "error",
        title: "No pudimos cancelar el turno",
        text: e?.message ?? "Intentá de nuevo.",
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <tr
        ref={rowRef}
        className={`${changed ? "is-changed " : ""}${a.status === "cancelled" ? "is-void" : ""}`}
      >
        <td className="num board__folio cell-folio">N° {folio(a.id)}</td>
        <td className="board__date cell-date">{formatDay(a.startAt)}</td>
        <td className="num board__time cell-time">{formatTime(a.startAt)}</td>
        <th scope="row" className="cell-who">
          <span className="board__primary">{doctorName}</span>
          {patientName ? (
            <span className="board__meta">{patientName}</span>
          ) : null}
        </th>
        <td className="cell-status">
          <span className="board__tags">
            <StatusTag status={a.status} />
            {changed ? <ChangedTag /> : null}
          </span>
        </td>
        <td className="cell-energy">
          <EnergyTag energy={a.price?.energy} />
        </td>
        <td className="num cell-total">
          {a.price ? formatArs(a.price.totalArs) : "—"}
        </td>
        <td className="board__action cell-action">
          {changed ? (
            <button
              type="button"
              className="btn btn--ghost btn--small"
              onClick={onSeen}
            >
              Visto
            </button>
          ) : null}
          {cancellable ? (
            <button
              type="button"
              className="btn btn--secondary btn--small"
              onClick={onReschedule}
            >
              Reprogramar
            </button>
          ) : null}
          {cancellable && !cancelling ? (
            <button
              type="button"
              className="btn btn--secondary btn--small"
              onClick={onCancelStart}
            >
              Cancelar
            </button>
          ) : null}
          {a.status === "booked" && !cancellable ? (
            <span className="board__meta">Faltan menos de 24 h</span>
          ) : null}
        </td>
      </tr>
      {cancelling ? (
        <tr className="board__drawer">
          <td colSpan={8}>
            <form className="cancel" onSubmit={submit}>
              <div className="field field--grow">
                <label htmlFor={`reason-${a.id}`}>
                  Motivo de la cancelación (opcional)
                </label>
                <input
                  id={`reason-${a.id}`}
                  value={reason}
                  maxLength={200}
                  onChange={(e) => setReason(e.target.value)}
                  autoFocus
                />
              </div>
              <button
                type="button"
                className="btn btn--ghost"
                onClick={() => onCancelDone(null)}
                disabled={busy}
              >
                Volver
              </button>
              <button type="submit" className="btn btn--danger" disabled={busy}>
                {busy ? "Cancelando…" : `Cancelar turno N° ${folio(a.id)}`}
              </button>
            </form>
          </td>
        </tr>
      ) : null}
    </>
  );
}

/**
 * FLIP: remember where each row was, and when the ranking changes animate it
 * from its old position to the new one, so a row moves instead of vanishing.
 */
function useFlip(order: string[]) {
  const nodes = useRef(new Map<string, HTMLTableRowElement>());
  const last = useRef(new Map<string, number>());
  const key = order.join("|");

  useLayoutEffect(() => {
    const reduce = window.matchMedia(
      "(prefers-reduced-motion: reduce)",
    ).matches;
    const next = new Map<string, number>();
    nodes.current.forEach((el, id) => {
      const top = el.getBoundingClientRect().top;
      next.set(id, top);
      const before = last.current.get(id);
      if (!reduce && before !== undefined && Math.abs(before - top) > 1) {
        el.animate(
          [
            { transform: `translateY(${before - top}px)` },
            { transform: "translateY(0)" },
          ],
          {
            duration: 420,
            easing: "cubic-bezier(0.16, 1, 0.3, 1)",
          },
        );
      }
    });
    last.current = next;
  }, [key]);

  return (id: string) => (el: HTMLTableRowElement | null) => {
    if (el) nodes.current.set(id, el);
    else nodes.current.delete(id);
  };
}
