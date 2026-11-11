import { useEffect, useState } from "react";
import { medicos } from "../../api/endpoints";
import type { Doctor, PatientProfile } from "../../api/types";
import type { Session } from "../../session";
import { BookingPanel } from "../booking/BookingPanel";
import { AppointmentsBoard } from "../appointments/AppointmentsBoard";
import { PatientSearch } from "../patients/PatientSearch";
import { Notice } from "../../ui/Notice";
import { isMinor } from "../../format";

export function StaffAppointments({ session }: { session: Session }) {
  const [doctors, setDoctors] = useState<Doctor[]>([]),
    [doctorId, setDoctorId] = useState(""),
    [patient, setPatient] = useState<PatientProfile | null>(null),
    [booking, setBooking] = useState(false),
    [revision, setRevision] = useState(0),
    [error, setError] = useState("");
  useEffect(() => {
    medicos
      .catalog({
        q: "",
        specialty: "",
        acceptsMinors: false,
        sort: "name",
        page: 1,
        size: 50,
      })
      .then((r) => setDoctors(r.items))
      .catch((e) => setError(String(e)));
  }, []);
  const doctor = doctors.find((d) => d.doctorId === doctorId);
  const canBook =
    !!patient?.enabled &&
    !!doctor &&
    (!isMinor(patient.birthDate) || doctor.acceptsMinors);
  return (
    <section className="staff-appointments">
      <h2>Agendas y asignación de turnos</h2>
      {error ? (
        <Notice tone="error" title="No pudimos cargar profesionales">
          {error}
        </Notice>
      ) : null}
      <div className="form-grid">
        <div className="field">
          <label htmlFor="staff-doctor">Profesional de la agenda</label>
          <select
            id="staff-doctor"
            value={doctorId}
            onChange={(e) => {
              setDoctorId(e.target.value);
              setBooking(false);
            }}
          >
            <option value="">Todos los profesionales</option>
            {doctors.map((d) => (
              <option key={d.doctorId} value={d.doctorId}>
                {d.fullName}
                {d.acceptsMinors ? " · atiende menores" : ""}
              </option>
            ))}
          </select>
        </div>
        <PatientSearch
          id="staff-book-patient"
          label="Paciente para asignar turno"
          value={patient}
          onChange={(p) => {
            setPatient(p);
            setBooking(false);
          }}
        />
      </div>
      {patient && !patient.enabled ? (
        <p role="status">
          Verificá y habilitá al paciente antes de asignar un turno.
        </p>
      ) : null}
      {patient &&
      doctor &&
      isMinor(patient.birthDate) &&
      !doctor.acceptsMinors ? (
        <p role="alert">
          Este profesional no atiende menores. Elegí otro profesional.
        </p>
      ) : null}
      <button
        className="btn btn--primary"
        disabled={!canBook}
        onClick={() => setBooking(true)}
      >
        Asignar turno al paciente seleccionado
      </button>
      {booking && doctor && patient ? (
        <div className="reschedule-panel">
          <BookingPanel
            key={`${doctorId}:${patient.patientId}`}
            session={session}
            doctor={doctor}
            patientId={patient.patientId}
            onClose={() => setBooking(false)}
            onBooked={() => setRevision((n) => n + 1)}
          />
        </div>
      ) : null}
      <AppointmentsBoard
        key={`${doctorId}:${revision}`}
        session={session}
        staff
        filters={{ doctorId: doctorId || undefined }}
      />
    </section>
  );
}
