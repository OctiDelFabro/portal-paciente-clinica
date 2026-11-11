import { useEffect, useState, type FormEvent } from "react";
import { turnos } from "../../api/endpoints";
import type { AgendaBlock, DoctorSchedule as Schedule } from "../../api/types";
import { formatDay, formatTime } from "../../format";
import { Notice } from "../../ui/Notice";

const DAYS = [
  "Domingo",
  "Lunes",
  "Martes",
  "Miércoles",
  "Jueves",
  "Viernes",
  "Sábado",
];
export function DoctorSchedule({
  doctorId,
  onChanged,
}: {
  doctorId: string;
  onChanged: () => void;
}) {
  const [schedule, setSchedule] = useState<Schedule | null>(null),
    [blocks, setBlocks] = useState<AgendaBlock[]>([]),
    [error, setError] = useState(""),
    [message, setMessage] = useState(""),
    [busy, setBusy] = useState(false),
    [start, setStart] = useState(""),
    [end, setEnd] = useState(""),
    [reason, setReason] = useState("");
  async function load() {
    try {
      const [s, b] = await Promise.all([
        turnos.schedule(doctorId),
        turnos.blocks(doctorId),
      ]);
      setSchedule(s);
      setBlocks(b.blocks);
    } catch (e) {
      setError(String(e));
    }
  }
  useEffect(() => {
    void load();
  }, [doctorId]);
  async function save(e: FormEvent) {
    e.preventDefault();
    if (!schedule) return;
    setBusy(true);
    setError("");
    try {
      await turnos.setSchedule(doctorId, schedule);
      setMessage(
        "Agenda actualizada. Las reservas existentes conservan su duración.",
      );
      onChanged();
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "No pudimos guardar la agenda.",
      );
    } finally {
      setBusy(false);
    }
  }
  async function block(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await turnos.block(
        doctorId,
        `${start}:00-03:00`,
        `${end}:00-03:00`,
        reason.trim(),
      );
      setMessage(
        "Bloqueo registrado. Los turnos afectados quedaron cancelados y sus avisos se procesan.",
      );
      setStart("");
      setEnd("");
      setReason("");
      await load();
      onChanged();
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "No pudimos bloquear la agenda.",
      );
    } finally {
      setBusy(false);
    }
  }
  async function release(b: AgendaBlock) {
    setBusy(true);
    setError("");
    try {
      await turnos.releaseBlock(doctorId, b.id);
      setMessage(
        "Bloqueo levantado. Los turnos cancelados no se vuelven a reservar automáticamente.",
      );
      await load();
      onChanged();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <details className="agenda-settings">
      <summary>Configurar horarios y bloquear agenda</summary>
      {error ? (
        <Notice tone="error" title="Revisá la agenda">
          {error}
        </Notice>
      ) : null}
      {message ? <Notice tone="alert" title={message} /> : null}
      {schedule ? (
        <form className="form" onSubmit={save}>
          <h2>Horarios de atención</h2>
          <fieldset>
            <legend>Días de atención</legend>
            <div className="check-grid">
              {DAYS.map((name, i) => (
                <label className="check" key={name}>
                  <input
                    type="checkbox"
                    checked={schedule.weekdays.includes(i)}
                    onChange={(e) =>
                      setSchedule({
                        ...schedule,
                        weekdays: e.target.checked
                          ? [...schedule.weekdays, i]
                          : schedule.weekdays.filter((d) => d !== i),
                      })
                    }
                  />
                  {name}
                </label>
              ))}
            </div>
          </fieldset>
          <div className="form-grid">
            <div className="field">
              <label htmlFor="schedule-open">Desde</label>
              <input
                id="schedule-open"
                type="time"
                value={schedule.open}
                onChange={(e) =>
                  setSchedule({ ...schedule, open: e.target.value })
                }
                required
              />
            </div>
            <div className="field">
              <label htmlFor="schedule-close">Hasta</label>
              <input
                id="schedule-close"
                type="time"
                value={schedule.close}
                onChange={(e) =>
                  setSchedule({ ...schedule, close: e.target.value })
                }
                required
              />
            </div>
            <div className="field">
              <label htmlFor="schedule-duration">
                Duración de nuevas reservas (minutos)
              </label>
              <input
                id="schedule-duration"
                type="number"
                min={5}
                max={240}
                value={schedule.durationMinutes}
                onChange={(e) =>
                  setSchedule({
                    ...schedule,
                    durationMinutes: Number(e.target.value),
                  })
                }
                required
              />
            </div>
          </div>
          <p>
            Todos los horarios se expresan en hora de la clínica, Argentina. Un
            cambio que deje reservas fuera de horario se rechaza; podés
            cancelarlas expresamente mediante un bloqueo.
          </p>
          <button
            className="btn btn--primary"
            disabled={busy || !schedule.weekdays.length}
          >
            Guardar horarios
          </button>
        </form>
      ) : null}
      <form className="form" onSubmit={block}>
        <h2>Bloquear un período</h2>
        <p>
          El bloqueo cancela los turnos superpuestos, incluso si faltan menos de
          24 horas. Los pacientes reciben el aviso.
        </p>
        <div className="form-grid">
          <div className="field">
            <label htmlFor="block-start">
              Inicio del bloqueo (hora de Argentina)
            </label>
            <input
              id="block-start"
              type="datetime-local"
              value={start}
              onChange={(e) => setStart(e.target.value)}
              required
            />
          </div>
          <div className="field">
            <label htmlFor="block-end">
              Fin del bloqueo (hora de Argentina)
            </label>
            <input
              id="block-end"
              type="datetime-local"
              value={end}
              min={start}
              onChange={(e) => setEnd(e.target.value)}
              required
            />
          </div>
        </div>
        <div className="field">
          <label htmlFor="block-reason">Motivo que recibirá el paciente</label>
          <input
            id="block-reason"
            value={reason}
            maxLength={200}
            onChange={(e) => setReason(e.target.value)}
            required
          />
        </div>
        <button
          className="btn btn--danger"
          disabled={busy || !start || !end || !reason.trim()}
        >
          Bloquear y cancelar turnos afectados
        </button>
      </form>
      <h2>Bloqueos vigentes</h2>
      {!blocks.length ? (
        <p>No hay bloqueos próximos.</p>
      ) : (
        <ul className="block-list">
          {blocks.map((b) => (
            <li key={b.id}>
              <span>
                {formatDay(b.startAt)} {formatTime(b.startAt)} —{" "}
                {formatDay(b.endAt)} {formatTime(b.endAt)} · {b.reason}
              </span>
              <button
                className="btn btn--secondary btn--small"
                disabled={busy}
                onClick={() => void release(b)}
              >
                Levantar bloqueo
              </button>
            </li>
          ))}
        </ul>
      )}
    </details>
  );
}
