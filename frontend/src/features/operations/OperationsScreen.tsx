import { useEffect, useState, type FormEvent } from "react";
import { request } from "../../api/http";
import { medicos, pacientes } from "../../api/endpoints";
import type { Doctor, PatientProfile } from "../../api/types";
import type { Session } from "../../session";
import { Notice } from "../../ui/Notice";
import { PatientForm } from "./PatientForm";
import { StaffAppointments } from "./StaffAppointments";
import { PatientSearch } from "../patients/PatientSearch";

export function OperationsScreen({ session }: { session: Session }) {
  const [revision, setRevision] = useState(0);
  const [patients, setPatients] = useState<PatientProfile[]>([]),
    [page, setPage] = useState(1),
    [next, setNext] = useState(0),
    [query, setQuery] = useState(""),
    [draft, setDraft] = useState(""),
    [message, setMessage] = useState(""),
    [busy, setBusy] = useState(false),
    [editing, setEditing] = useState<PatientProfile | null>(null),
    [child, setChild] = useState<PatientProfile | null>(null),
    [adult, setAdult] = useState<PatientProfile | null>(null);
  async function load() {
    try {
      const r = await pacientes.patients(page, query);
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
  }, [page, query]);
  async function verify(p: PatientProfile) {
    setBusy(true);
    setMessage("");
    try {
      await request(
        "PATCH",
        `/api/pacientes/patients/${encodeURIComponent(p.patientId)}/verification`,
        { enabled: !p.enabled },
      );
      await load();
      setRevision((n) => n + 1);
    } catch (e) {
      setMessage(
        e instanceof Error ? e.message : "No pudimos verificar los datos.",
      );
    } finally {
      setBusy(false);
    }
  }
  async function link(e: FormEvent) {
    e.preventDefault();
    if (!child) return;
    setBusy(true);
    setMessage("");
    try {
      await request(
        "PATCH",
        `/api/pacientes/patients/${encodeURIComponent(child.patientId)}/responsible`,
        { responsiblePatientId: adult?.patientId ?? "" },
      );
      setMessage(
        adult ? "Vínculo verificado actualizado." : "Vínculo retirado.",
      );
      await load();
      setRevision((n) => n + 1);
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
      <details className="agenda-settings">
        <summary>Registrar un paciente</summary>
        <PatientForm onSaved={() => void load()} />
      </details>
      <form
        className="toolbar"
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          setPage(1);
          setQuery(draft.trim());
        }}
      >
        <div className="field field--grow">
          <label htmlFor="patient-management-query">
            Buscar pacientes por nombre, DNI o correo
          </label>
          <input
            id="patient-management-query"
            type="search"
            value={draft}
            maxLength={120}
            onChange={(e) => setDraft(e.target.value)}
          />
        </div>
        <button className="btn btn--primary">Buscar pacientes</button>
      </form>
      <div className="board">
        <table className="board__table">
          <thead>
            <tr>
              <th>Paciente</th>
              <th>DNI</th>
              <th>Correo</th>
              <th>Responsable</th>
              <th>Acciones</th>
            </tr>
          </thead>
          <tbody>
            {patients.map((p) => (
              <tr key={p.patientId}>
                <th scope="row">{p.fullName}</th>
                <td>{p.documentNumber || "Sin registrar"}</td>
                <td>{p.email ?? "Sin correo"}</td>
                <td>{p.responsiblePatientId || "—"}</td>
                <td className="board__action">
                  <button
                    className="btn btn--secondary btn--small"
                    disabled={busy}
                    onClick={() => void verify(p)}
                  >
                    {p.enabled ? "Inhabilitar" : "Verificar y habilitar"}
                  </button>
                  <button
                    className="btn btn--ghost btn--small"
                    onClick={() => setEditing(p)}
                  >
                    Editar datos
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
          onClick={() => setPage((p) => p - 1)}
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
      {editing ? (
        <PatientForm
          key={editing.patientId}
          patient={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            void load();
          }}
        />
      ) : null}
      <form className="form" onSubmit={link}>
        <h2>Vincular responsable</h2>
        <div className="form-grid">
          <PatientSearch
            id="child-id"
            label="Menor"
            value={child}
            onChange={setChild}
            minorOnly
          />
          <PatientSearch
            id="adult-id"
            label="Adulto responsable verificado"
            value={adult}
            onChange={setAdult}
            adultOnly
          />
        </div>
        <p>
          Para retirar el vínculo, elegí al menor y dejá el adulto sin
          seleccionar.
        </p>
        <button className="btn btn--primary" disabled={busy || !child}>
          Registrar vínculo verificado
        </button>
      </form>
      <StaffAppointments key={revision} session={session} />
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
