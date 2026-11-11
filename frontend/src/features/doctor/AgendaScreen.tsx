import { DoctorSchedule } from "./DoctorSchedule";
import { useEffect, useState, type FormEvent } from "react";
import { RefreshCw } from "lucide-react";
import { historia, pacientes } from "../../api/endpoints";
import { ApiError } from "../../api/http";
import type { Appointment } from "../../api/types";
import { folio, formatDay, formatTime } from "../../format";
import { Notice } from "../../ui/Notice";
import { StatusTag } from "../../ui/StatusTag";
import { HistoryScreen } from "../history/HistoryScreen";
import { useAppointments } from "../appointments/useAppointments";

/** Doctor view: assigned appointments as a board, and a note per encounter. */
export function AgendaScreen({ doctorId }: { doctorId: string }) {
  const { items, state, refresh, hasMore, refreshing, loadMore } =
    useAppointments();
  const names = usePatientNames(items.map((a) => a.patientId));
  const [open, setOpen] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);

  return (
    <section className="single" aria-labelledby="agenda-title">
      <header className="screen-head screen-head--row">
        <div>
          <h1 id="agenda-title" className="screen-title">
            Mi agenda
          </h1>
          <p className="screen-lede">
            Tus turnos asignados por fecha. Registrá la atención en la historia
            clínica del paciente.
          </p>
        </div>
        <button
          type="button"
          className="btn btn--ghost"
          onClick={() => void refresh()}
        >
          <RefreshCw size={16} aria-hidden="true" /> Actualizar
        </button>
      </header>

      <DoctorSchedule doctorId={doctorId} onChanged={() => void refresh()} />
      {state.kind === "error" ? (
        <Notice
          tone={state.unavailable ? "alert" : "error"}
          title="No pudimos traer tu agenda"
        >
          {state.unavailable
            ? "El servicio de turnos no responde."
            : state.message}
        </Notice>
      ) : null}
      {saved ? <Notice tone="alert" title={saved} /> : null}

      <div className="board" aria-busy={state.kind === "loading"}>
        <table className="board__table">
          <caption className="visually-hidden">Turnos asignados</caption>
          <thead>
            <tr>
              <th scope="col">Folio</th>
              <th scope="col">Fecha</th>
              <th scope="col">Hora</th>
              <th scope="col">Paciente</th>
              <th scope="col">Estado</th>
              <th scope="col">
                <span className="visually-hidden">Acciones</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {state.kind === "ready" && items.length === 0 ? (
              <tr>
                <td colSpan={6} className="board__empty">
                  No tenés turnos asignados.
                </td>
              </tr>
            ) : null}
            {items.map((a) => (
              <AgendaRow
                key={a.id}
                appointment={a}
                patientName={names[a.patientId] ?? a.patientId}
                open={open === a.id}
                onToggle={() => {
                  setSaved(null);
                  setOpen(open === a.id ? null : a.id);
                }}
                onSaved={() => {
                  setOpen(null);
                  setSaved(
                    `Nota guardada en la historia de ${names[a.patientId] ?? a.patientId}`,
                  );
                }}
              />
            ))}
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
    </section>
  );
}

function AgendaRow({
  appointment: a,
  patientName,
  open,
  onToggle,
  onSaved,
}: {
  appointment: Appointment;
  patientName: string;
  open: boolean;
  onToggle: () => void;
  onSaved: () => void;
}) {
  const [showHistory, setShowHistory] = useState(false);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await historia.addNote(a.id, text.trim());
      setText("");
      onSaved();
    } catch (err) {
      const e = err instanceof ApiError ? err : null;
      if (e?.status === 409)
        setError("El turno fue cancelado: no admite notas.");
      else if (e?.status === 404)
        setError(
          "La historia todavía no registró este turno. Probá en unos segundos.",
        );
      else if (e?.unavailable)
        setError(
          "No pudimos confirmar si la nota se guardó. Revisá la historia antes de repetirla.",
        );
      else setError(e?.message ?? "No pudimos guardar la nota.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <tr className={a.status === "cancelled" ? "is-void" : undefined}>
        <td className="num board__folio">N° {folio(a.id)}</td>
        <td className="num">{formatDay(a.startAt)}</td>
        <td className="num board__time">{formatTime(a.startAt)}</td>
        <th scope="row">
          <span className="board__primary">{patientName}</span>
        </th>
        <td>
          <StatusTag status={a.status} />
        </td>
        <td className="board__action">
          {a.status === "booked" ? (
            <button
              type="button"
              className="btn btn--secondary btn--small"
              disabled={Date.parse(a.startAt) > Date.now()}
              title={
                Date.parse(a.startAt) > Date.now()
                  ? "Disponible a partir del inicio del turno"
                  : undefined
              }
              aria-expanded={open}
              onClick={onToggle}
            >
              {open ? "Cerrar" : "Registrar atención"}
            </button>
          ) : null}
        </td>
      </tr>
      {open ? (
        <tr className="board__drawer">
          <td colSpan={6}>
            <button
              type="button"
              className="btn btn--ghost"
              onClick={() => setShowHistory(!showHistory)}
            >
              {showHistory ? "Ocultar historia" : "Consultar historia clínica"}
            </button>
            {showHistory ? <HistoryScreen patientId={a.patientId} /> : null}
            <form className="note-form" onSubmit={submit}>
              <div className="field field--grow">
                <label htmlFor={`note-${a.id}`}>Nota de la atención</label>
                <textarea
                  id={`note-${a.id}`}
                  rows={4}
                  maxLength={4000}
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  aria-describedby={`note-count-${a.id}`}
                  autoFocus
                />
                <p id={`note-count-${a.id}`} className="field__hint num">
                  {text.length} / 4000
                </p>
              </div>
              {error ? (
                <p className="form__error" role="alert">
                  {error}
                </p>
              ) : null}
              <div className="note-form__actions">
                <button
                  type="submit"
                  className="btn btn--primary"
                  disabled={busy || text.trim() === ""}
                >
                  {busy ? "Guardando…" : "Guardar nota"}
                </button>
              </div>
            </form>
          </td>
        </tr>
      ) : null}
    </>
  );
}

function usePatientNames(ids: string[]): Record<string, string> {
  const [names, setNames] = useState<Record<string, string>>({});
  const key = [...new Set(ids)].sort().join(",");
  useEffect(() => {
    let alive = true;
    const unique = key ? key.split(",") : [];
    Promise.all(
      unique.map((id) =>
        pacientes
          .contact(id)
          .then((c) => [id, c.fullName] as const)
          .catch(() => [id, id] as const),
      ),
    ).then((pairs) => alive && setNames(Object.fromEntries(pairs)));
    return () => {
      alive = false;
    };
  }, [key]);
  return names;
}
