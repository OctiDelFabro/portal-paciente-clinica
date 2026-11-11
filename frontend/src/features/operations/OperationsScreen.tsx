import { useEffect, useState, type FormEvent } from "react";
import { request } from "../../api/http";
import { medicos } from "../../api/endpoints";
import type { Doctor } from "../../api/types";
import type { Session } from "../../session";
import { Notice } from "../../ui/Notice";

type Patient = {
  patientId: string;
  fullName: string;
  email: string | null;
  enabled: boolean;
  responsiblePatientId: string;
};
export function OperationsScreen({ session }: { session: Session }) {
  const [patients, setPatients] = useState<Patient[]>([]);
  const [page, setPage] = useState(1);
  const [next, setNext] = useState(0);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [child, setChild] = useState("");
  const [adult, setAdult] = useState("");
  async function load() {
    try {
      const r = await request<{ patients: Patient[]; nextPage: number }>(
        "GET",
        `/api/pacientes/patients?page=${page}`,
      );
      setPatients(r.patients);
      setNext(r.nextPage);
    } catch (e) {
      setMessage(
        e instanceof Error ? e.message : "No pudimos consultar pacientes.",
      );
    }
  }
  useEffect(() => {
    void load();
  }, [page]);
  async function verify(p: Patient) {
    setBusy(true);
    setMessage("");
    try {
      await request(
        "PATCH",
        `/api/pacientes/patients/${encodeURIComponent(p.patientId)}/verification`,
        { enabled: !p.enabled },
      );
      await load();
    } catch (e) {
      setMessage(
        e instanceof Error
          ? e.message
          : "No pudimos actualizar la verificación.",
      );
    } finally {
      setBusy(false);
    }
  }
  async function link(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      await request(
        "PATCH",
        `/api/pacientes/patients/${encodeURIComponent(child)}/responsible`,
        { responsiblePatientId: adult },
      );
      setMessage("Vínculo actualizado.");
      await load();
    } catch (e) {
      setMessage(
        e instanceof Error ? e.message : "No pudimos registrar el vínculo.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="single">
      <header className="screen-head">
        <h1 className="screen-title">Gestión de pacientes</h1>
        <p className="screen-lede">
          Verificá los datos antes de habilitar una cuenta. Los vínculos se
          registran después de comprobar la documentación.
        </p>
      </header>
      {message ? <Notice tone="alert" title={message} /> : null}
      <div className="board">
        <table className="board__table">
          <thead>
            <tr>
              <th>Paciente</th>
              <th>Identificador</th>
              <th>Correo</th>
              <th>Responsable</th>
              <th>Acción</th>
            </tr>
          </thead>
          <tbody>
            {patients.map((p) => (
              <tr key={p.patientId}>
                <th scope="row">{p.fullName}</th>
                <td>{p.patientId}</td>
                <td>{p.email ?? "Sin correo"}</td>
                <td>{p.responsiblePatientId || "—"}</td>
                <td>
                  <button
                    className="btn btn--secondary btn--small"
                    disabled={busy}
                    onClick={() => void verify(p)}
                  >
                    {p.enabled ? "Inhabilitar" : "Verificar y habilitar"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="pager">
        <button
          className="btn btn--ghost"
          disabled={page === 1}
          onClick={() => setPage(page - 1)}
        >
          Anterior
        </button>
        <span>Página {page}</span>
        <button
          className="btn btn--ghost"
          disabled={!next}
          onClick={() => setPage(next)}
        >
          Siguiente
        </button>
      </div>
      <form className="form" onSubmit={link}>
        <h2>Vincular responsable</h2>
        <div className="field">
          <label htmlFor="child-id">Identificador del menor</label>
          <input
            id="child-id"
            value={child}
            onChange={(e) => setChild(e.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="adult-id">
            Identificador del adulto verificado (vacío para quitar)
          </label>
          <input
            id="adult-id"
            value={adult}
            onChange={(e) => setAdult(e.target.value)}
          />
        </div>
        <button className="btn btn--primary" disabled={busy}>
          Registrar vínculo verificado
        </button>
      </form>
      {session.role === "admin" ? <DoctorAdministration /> : null}
    </section>
  );
}
function DoctorAdministration() {
  const [id, setId] = useState("");
  const [doctor, setDoctor] = useState<Doctor | null>(null);
  const [specialties, setSpecialties] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function load() {
    try {
      const d = await medicos.doctor(id);
      setDoctor(d);
      setSpecialties(d.specialties.join(","));
    } catch (e) {
      setMessage(String(e));
    }
  }
  async function save(e: FormEvent) {
    e.preventDefault();
    if (!doctor) return;
    setBusy(true);
    try {
      await request("PUT", `/api/medicos/doctors/${encodeURIComponent(id)}`, {
        fullName: doctor.fullName,
        specialties: specialties
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
        active: doctor.active,
        acceptsMinors: doctor.acceptsMinors,
      });
      setMessage("Perfil actualizado; la búsqueda se sincroniza en segundos.");
    } catch (e) {
      setMessage(String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div>
      <h2>Administrar profesionales</h2>
      <div className="field">
        <label htmlFor="doctor-admin-id">Identificador del profesional</label>
        <input
          id="doctor-admin-id"
          value={id}
          placeholder="Ej.: doc-001"
          onChange={(e) => {
            setId(e.target.value);
            setDoctor(null);
          }}
        />
      </div>
      <button
        className="btn btn--secondary"
        disabled={!id.trim()}
        onClick={() => void load()}
      >
        Consultar perfil
      </button>
      {message ? <Notice tone="alert" title={message} /> : null}
      {doctor ? (
        <form className="form" onSubmit={save}>
          <div className="field">
            <label htmlFor="doctor-admin-name">Nombre</label>
            <input
              id="doctor-admin-name"
              value={doctor.fullName}
              onChange={(e) =>
                setDoctor({ ...doctor, fullName: e.target.value })
              }
              required
            />
          </div>
          <div className="field">
            <label htmlFor="doctor-admin-specialties">
              Códigos de especialidad separados por coma
            </label>
            <input
              id="doctor-admin-specialties"
              value={specialties}
              onChange={(e) => setSpecialties(e.target.value)}
              required
            />
          </div>
          <label className="check">
            <input
              type="checkbox"
              checked={doctor.active}
              onChange={(e) =>
                setDoctor({ ...doctor, active: e.target.checked })
              }
            />
            Activo
          </label>
          <label className="check">
            <input
              type="checkbox"
              checked={doctor.acceptsMinors}
              onChange={(e) =>
                setDoctor({ ...doctor, acceptsMinors: e.target.checked })
              }
            />
            Atiende menores
          </label>
          <button className="btn btn--primary" disabled={busy}>
            Guardar perfil
          </button>
        </form>
      ) : null}
      <button
        className="btn btn--ghost"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          try {
            await request("POST", "/api/medicos/admin/reindex");
            setMessage("Índice reconstruido.");
          } catch (e) {
            setMessage(String(e));
          } finally {
            setBusy(false);
          }
        }}
      >
        Reconstruir búsqueda
      </button>
    </div>
  );
}
