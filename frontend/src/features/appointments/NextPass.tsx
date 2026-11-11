import { usePatientTarget } from "../patients/PatientContext";
import { useEffect, useState } from "react";
import { turnos } from "../../api/endpoints";
import type { Appointment } from "../../api/types";
import { specialtyName } from "../../format";
import { Pass } from "../../ui/Pass";
import { useDoctors } from "../doctors";

/** The side column's resting state: the next confirmed appointment, as a pass. */
export function NextPass() {
  const target = usePatientTarget()?.patientId;
  const [next, setNext] = useState<Appointment | null | undefined>(undefined);

  useEffect(() => {
    let alive = true;
    turnos
      .list(target)
      .then((list) => {
        const now = Date.now();
        const upcoming = list
          .filter(
            (a) => a.status === "booked" && new Date(a.startAt).getTime() > now,
          )
          .sort((a, b) => a.startAt.localeCompare(b.startAt));
        if (alive) setNext(upcoming[0] ?? null);
      })
      .catch(() => alive && setNext(null));
    return () => {
      alive = false;
    };
  }, [target]);

  const doctors = useDoctors(next ? [next.doctorId] : []);

  if (next === undefined)
    return <div className="side-empty" aria-busy="true" />;
  if (next === null) {
    return (
      <div className="side-empty">
        <p className="side-empty__title">Todavía no tenés turnos próximos</p>
        <p className="side-empty__text">
          Buscá un profesional y elegí un horario: el pase aparece acá.
        </p>
      </div>
    );
  }
  const doctor = doctors[next.doctorId];
  return (
    <section aria-labelledby="next-title">
      <h2 id="next-title" className="side-title">
        Próximo turno
      </h2>
      <Pass
        label="Pase del próximo turno"
        appointmentId={next.id}
        doctorName={doctor?.fullName ?? next.doctorId}
        specialties={(doctor?.specialties ?? []).map(specialtyName)}
        startAt={next.startAt}
        endAt={next.endAt}
        status={next.status}
        price={next.price}
      />
    </section>
  );
}
