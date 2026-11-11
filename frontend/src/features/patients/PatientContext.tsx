import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { pacientes } from "../../api/endpoints";
import type { PatientProfile, Profile } from "../../api/types";
import type { Session } from "../../session";
import { Notice } from "../../ui/Notice";

type Target = {
  selfId: string;
  patientId: string;
  setPatientId: (id: string) => void;
  profile: Profile | null;
  dependents: PatientProfile[];
  refresh: () => Promise<void>;
  error: string;
};
const Context = createContext<Target | null>(null);
export const usePatientTarget = () => useContext(Context);

export function PatientProvider({
  session,
  children,
}: {
  session: Session;
  children: ReactNode;
}) {
  const [profile, setProfile] = useState<Profile | null>(null),
    [dependents, setDependents] = useState<PatientProfile[]>([]),
    [patientId, setPatientId] = useState(session.subject),
    [error, setError] = useState("");
  async function refresh() {
    try {
      const p = await pacientes.me();
      setProfile(p);
      setError("");
      if (p.enabled) {
        try {
          const r = await pacientes.dependents();
          setDependents(r.patients);
        } catch {
          setDependents([]);
        }
      } else setDependents([]);
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "No pudimos verificar la cuenta.",
      );
    }
  }
  useEffect(() => {
    let alive = true;
    async function load() {
      try {
        const p = await pacientes.me();
        if (!alive) return;
        setProfile(p);
        setError("");
        const deps = p.enabled
          ? await pacientes.dependents().catch(() => ({ patients: [] }))
          : { patients: [] };
        if (alive) setDependents(deps.patients);
      } catch (e) {
        if (alive)
          setError(
            e instanceof Error ? e.message : "No pudimos verificar la cuenta.",
          );
      }
    }
    void load();
    const timer = window.setInterval(() => void load(), 30000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, [session.subject]);
  useEffect(() => {
    if (
      patientId !== session.subject &&
      !dependents.some((p) => p.patientId === patientId)
    )
      setPatientId(session.subject);
  }, [dependents, patientId, session.subject]);
  return (
    <Context.Provider
      value={{
        selfId: session.subject,
        patientId,
        setPatientId,
        profile,
        dependents,
        refresh,
        error,
      }}
    >
      {children}
    </Context.Provider>
  );
}

export function PatientPicker() {
  const t = usePatientTarget();
  if (!t) return null;
  return (
    <div className="patient-context">
      {t.dependents.length > 0 ? (
        <div className="field">
          <label htmlFor="patient-target">Atención para</label>
          <select
            id="patient-target"
            value={t.patientId}
            onChange={(e) => t.setPatientId(e.target.value)}
          >
            <option value={t.selfId}>
              Mi cuenta · {t.profile?.fullName ?? "Yo"}
            </option>
            {t.dependents.map((p) => (
              <option key={p.patientId} value={p.patientId}>
                {p.fullName} · menor a cargo
              </option>
            ))}
          </select>
        </div>
      ) : null}
      {t.profile && !t.profile.enabled ? (
        <Notice tone="alert" title="Cuenta pendiente de habilitación">
          Podés buscar profesionales. Recepción debe verificar tus datos antes
          de reservar o consultar historias clínicas.{" "}
          <button
            className="btn btn--ghost btn--small"
            onClick={() => void t.refresh()}
          >
            Actualizar estado
          </button>
        </Notice>
      ) : null}
      {t.error ? (
        <Notice tone="error" title="No pudimos verificar la cuenta">
          {t.error}{" "}
          <button
            className="btn btn--ghost btn--small"
            onClick={() => void t.refresh()}
          >
            Reintentar
          </button>
        </Notice>
      ) : null}
    </div>
  );
}
