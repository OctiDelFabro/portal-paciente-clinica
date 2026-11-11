import { useEffect, useState } from "react";
import { notifications } from "../../api/endpoints";
import type { PortalNotice } from "../../api/types";
import { formatDay, formatTime } from "../../format";
import { Notice } from "../../ui/Notice";
import { usePatientTarget } from "../patients/PatientContext";
import { useDoctors } from "../doctors";

const LABELS: Record<string, string> = {
  appointment_booked: "Turno confirmado",
  appointment_cancelled: "Turno cancelado",
  appointment_rescheduled: "Turno reprogramado",
  appointment_reminder: "Recordatorio de turno",
};
export function NotificationsScreen() {
  const target = usePatientTarget();
  const [items, setItems] = useState<PortalNotice[]>([]),
    [page, setPage] = useState(1),
    [more, setMore] = useState(0),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [revision, setRevision] = useState(0);
  const doctors = useDoctors(items.map((n) => n.doctorId).filter(Boolean));
  useEffect(() => {
    let alive = true;
    setBusy(true);
    notifications
      .page(page, target?.patientId)
      .then((r) => {
        if (!alive) return;
        setItems((old) =>
          page > 1
            ? [
                ...new Map(
                  [...old, ...r.notices].map((n) => [n.id, n]),
                ).values(),
              ]
            : r.notices,
        );
        setMore(r.nextPage);
        setError("");
      })
      .catch((e) => {
        if (alive)
          setError(
            e instanceof Error ? e.message : "No pudimos traer los avisos.",
          );
      })
      .finally(() => {
        if (alive) setBusy(false);
      });
    return () => {
      alive = false;
    };
  }, [page, target?.patientId, revision]);
  async function read(n: PortalNotice) {
    try {
      await notifications.read(n.id, target?.patientId);
      setItems((old) =>
        old.map((x) =>
          x.id === n.id ? { ...x, readAt: new Date().toISOString() } : x,
        ),
      );
    } catch (e) {
      setError(String(e));
    }
  }
  return (
    <section className="single single--narrow">
      <header className="screen-head screen-head--row">
        <div>
          <h1 className="screen-title">Avisos</h1>
          <p className="screen-lede">
            Confirmaciones, cancelaciones, reprogramaciones y recordatorios de
            tus turnos.
          </p>
        </div>
        <button
          className="btn btn--ghost"
          onClick={() => {
            setPage(1);
            setRevision((n) => n + 1);
          }}
          disabled={busy}
        >
          Actualizar avisos
        </button>
      </header>
      {error ? (
        <Notice tone="error" title="No pudimos cargar los avisos">
          {error}
        </Notice>
      ) : null}
      {!busy && !error && !items.length ? (
        <p className="side-empty">Todavía no tenés avisos.</p>
      ) : null}
      <ol className="notice-list">
        {items.map((n) => (
          <li
            key={n.id}
            className={n.readAt ? "notice-item" : "notice-item is-unread"}
          >
            <div>
              <h2>{LABELS[n.kind] ?? "Actualización de turno"}</h2>
              <p>
                {n.doctorId
                  ? (doctors[n.doctorId]?.fullName ?? n.doctorId)
                  : "Tu consulta"}
                {n.startAt
                  ? ` · ${formatDay(n.startAt)} ${formatTime(n.startAt)}`
                  : ""}
              </p>
              {n.reason ? (
                <p>
                  {n.reason === "rescheduled"
                    ? "Turno reemplazado por una nueva reserva."
                    : n.reason}
                </p>
              ) : null}
              <p className="board__meta">
                {formatDay(n.createdAt)} {formatTime(n.createdAt)}
              </p>
            </div>
            {!n.readAt ? (
              <button
                className="btn btn--secondary btn--small"
                onClick={() => void read(n)}
              >
                Marcar leído
              </button>
            ) : (
              <span className="board__meta">Leído</span>
            )}
          </li>
        ))}
      </ol>
      {more ? (
        <button
          className="btn btn--secondary"
          disabled={busy}
          onClick={() => setPage(more)}
        >
          Cargar más avisos
        </button>
      ) : null}
    </section>
  );
}
