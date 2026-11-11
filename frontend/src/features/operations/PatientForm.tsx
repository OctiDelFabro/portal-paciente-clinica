import { useState, type FormEvent } from "react";
import { pacientes } from "../../api/endpoints";
import type { PatientProfile } from "../../api/types";
import { clinicDate } from "../../format";

export function PatientForm({
  patient,
  onSaved,
  onClose,
}: {
  patient?: PatientProfile;
  onSaved: () => void;
  onClose?: () => void;
}) {
  const [form, setForm] = useState({
      fullName: patient?.fullName ?? "",
      email: patient?.email ?? "",
      documentNumber: patient?.documentNumber ?? "",
      birthDate: patient?.birthDate ?? "",
    }),
    [password, setPassword] = useState(""),
    [enabled, setEnabled] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [success, setSuccess] = useState("");
  const prefix = patient ? "edit-patient" : "new-patient";
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      if (patient) await pacientes.update(patient.patientId, form);
      else
        await pacientes.create({
          ...form,
          enabled,
          ...(password ? { password } : {}),
        });
      setSuccess(patient ? "Datos actualizados." : "Paciente registrado.");
      setPassword("");
      onSaved();
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "No pudimos guardar los datos.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="form patient-form" onSubmit={submit}>
      <h2>
        {patient ? `Editar datos de ${patient.fullName}` : "Registrar paciente"}
      </h2>
      <div className="form-grid">
        <div className="field">
          <label htmlFor={`${prefix}-name`}>Nombre y apellido</label>
          <input
            id={`${prefix}-name`}
            value={form.fullName}
            maxLength={120}
            onChange={(e) => setForm({ ...form, fullName: e.target.value })}
            required
          />
        </div>
        <div className="field">
          <label htmlFor={`${prefix}-document`}>DNI del paciente</label>
          <input
            id={`${prefix}-document`}
            value={form.documentNumber}
            minLength={6}
            maxLength={20}
            pattern="[A-Za-z0-9]{6,20}"
            onChange={(e) =>
              setForm({ ...form, documentNumber: e.target.value })
            }
            required
          />
        </div>
        <div className="field">
          <label htmlFor={`${prefix}-birth`}>
            Fecha de nacimiento del paciente
          </label>
          <input
            id={`${prefix}-birth`}
            type="date"
            value={form.birthDate}
            max={clinicDate(new Date())}
            onChange={(e) => setForm({ ...form, birthDate: e.target.value })}
            required
          />
        </div>
        <div className="field">
          <label htmlFor={`${prefix}-email`}>
            Correo del paciente (opcional)
          </label>
          <input
            id={`${prefix}-email`}
            type="email"
            value={form.email}
            onChange={(e) => setForm({ ...form, email: e.target.value })}
          />
        </div>
      </div>
      {!patient ? (
        <>
          <div className="field">
            <label htmlFor="initial-patient-password">
              Contraseña inicial para el portal (opcional)
            </label>
            <input
              id="initial-patient-password"
              type="password"
              value={password}
              minLength={8}
              maxLength={72}
              autoComplete="new-password"
              onChange={(e) => setPassword(e.target.value)}
            />
            <p className="field__hint">
              El acceso al portal requiere correo y contraseña. Una ficha sin
              acceso también puede ser gestionada por recepción.
            </p>
          </div>
          <label className="check">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
            />
            Datos verificados: habilitar para reservar
          </label>
        </>
      ) : null}
      {error ? (
        <p className="form__error" role="alert">
          {error}
        </p>
      ) : null}
      {success ? <p role="status">{success}</p> : null}
      <div className="note-form__actions">
        <button className="btn btn--primary" disabled={busy}>
          {busy ? "Guardando…" : "Guardar paciente"}
        </button>
        {onClose ? (
          <button type="button" className="btn btn--ghost" onClick={onClose}>
            Cerrar edición
          </button>
        ) : null}
      </div>
    </form>
  );
}
