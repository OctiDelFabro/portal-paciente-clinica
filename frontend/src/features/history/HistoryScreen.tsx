import { usePatientTarget } from "../patients/PatientContext";
import { useEffect, useState } from "react";
import { historia } from "../../api/endpoints";
import { ApiError } from "../../api/http";
import type { ClinicalRecord } from "../../api/types";
import { folio, formatDay, formatLongDay, formatTime } from "../../format";
import { Notice } from "../../ui/Notice";
import { useDoctors } from "../doctors";

type State =
  | { kind: "loading" }
  | { kind: "ready"; records: ClinicalRecord[] }
  | { kind: "error"; message: string; unavailable: boolean };

export function HistoryScreen({ patientId }: { patientId?: string } = {}) {
  const target = usePatientTarget();
  patientId =
    patientId ??
    (target && target.patientId !== target.selfId
      ? target.patientId
      : undefined);
  const [state, setState] = useState<State>({ kind: "loading" });

  useEffect(() => {
    (patientId ? historia.patient(patientId) : historia.mine())
      .then((h) => setState({ kind: "ready", records: h.records }))
      .catch((err: unknown) => {
        const e = err instanceof ApiError ? err : null;
        setState({
          kind: "error",
          message:
            e?.code === "PATIENT_NOT_ENABLED"
              ? "Recepción debe verificar y habilitar tu cuenta antes de consultar historias clínicas."
              : (e?.message ?? "No pudimos traer tu historia clínica."),
          unavailable: e?.unavailable ?? false,
        });
      });
  }, [patientId]);

  const records = state.kind === "ready" ? state.records : [];
  const doctors = useDoctors(records.map((r) => r.doctorId));

  return (
    <section className="single single--narrow" aria-labelledby="history-title">
      <header className="screen-head">
        <h1 id="history-title" className="screen-title">
          Historia clínica
        </h1>
        <p className="screen-lede">
          {patientId
            ? `Atenciones del paciente ${patientId}, con las notas registradas por los profesionales.`
            : "Tus atenciones en la clínica, de la más reciente a la más antigua, con las notas de cada profesional."}
        </p>
      </header>

      {state.kind === "loading" ? (
        <div className="timeline__skeleton" aria-busy="true" />
      ) : null}
      {state.kind === "error" ? (
        <Notice
          tone={state.unavailable ? "alert" : "error"}
          title="La historia clínica no está disponible ahora"
        >
          {state.unavailable
            ? "El servicio no responde. Tus turnos siguen funcionando; volvé a intentar en unos minutos."
            : state.message}
        </Notice>
      ) : null}
      {state.kind === "ready" && records.length === 0 ? (
        <div className="side-empty">
          <p className="side-empty__title">
            Todavía no hay atenciones registradas
          </p>
          <p className="side-empty__text">
            Cuando reserves un turno aparece acá, y el profesional agrega sus
            notas después de atenderte.
          </p>
        </div>
      ) : null}

      <ol className="timeline">
        {records.map((r) => (
          <li
            key={r.appointmentId}
            className={`stub${r.status === "cancelled" ? " stub--void" : ""}`}
          >
            <div className="stub__rail">
              <span className="stub__day num">{formatDay(r.startAt)}</span>
              <span className="stub__time num">{formatTime(r.startAt)}</span>
            </div>
            <div className="stub__body">
              <header className="stub__head">
                <span className="board__primary">
                  {doctors[r.doctorId]?.fullName ?? r.doctorId}
                </span>
                <span className="num stub__folio">
                  N° {folio(r.appointmentId)}
                </span>
              </header>
              <p className="stub__meta">
                {formatLongDay(r.startAt)} ·{" "}
                {r.status === "cancelled"
                  ? `Cancelado${r.cancelReason ? `: ${r.cancelReason}` : ""}`
                  : "Turno registrado"}
              </p>
              {r.notes.length > 0 ? (
                <ul className="notes">
                  {r.notes.map((n, i) => (
                    <li key={n.noteId ?? i} className="note">
                      <p className="note__text">{n.text}</p>
                      <p className="note__meta num">
                        {formatDay(n.createdAt)} {formatTime(n.createdAt)}
                      </p>
                    </li>
                  ))}
                </ul>
              ) : r.status !== "cancelled" ? (
                <p className="stub__empty">
                  Sin notas del profesional todavía.
                </p>
              ) : null}
            </div>
          </li>
        ))}
      </ol>
    </section>
  );
}
