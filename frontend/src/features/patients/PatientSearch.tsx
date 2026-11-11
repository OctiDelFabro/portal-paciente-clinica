import { useEffect, useState } from "react";
import { pacientes } from "../../api/endpoints";
import type { PatientProfile } from "../../api/types";
import { isMinor } from "../../format";

export function PatientSearch({
  id,
  label,
  value,
  onChange,
  minorOnly = false,
  adultOnly = false,
}: {
  id: string;
  label: string;
  value: PatientProfile | null;
  onChange: (p: PatientProfile | null) => void;
  minorOnly?: boolean;
  adultOnly?: boolean;
}) {
  const [query, setQuery] = useState(""),
    [options, setOptions] = useState<PatientProfile[]>([]),
    [more, setMore] = useState(false),
    [error, setError] = useState("");
  useEffect(() => {
    let alive = true;
    const timer = window.setTimeout(() => {
      pacientes
        .patients(1, query.trim())
        .then((r) => {
          if (alive) {
            setOptions(
              r.patients.filter(
                (p) =>
                  (!minorOnly || isMinor(p.birthDate)) &&
                  (!adultOnly || (p.enabled && !isMinor(p.birthDate))),
              ),
            );
            setMore(!!r.nextPage);
            setError("");
          }
        })
        .catch((e) => {
          if (alive) setError(String(e));
        });
    }, 250);
    return () => {
      alive = false;
      window.clearTimeout(timer);
    };
  }, [query, minorOnly, adultOnly]);
  const visible =
    value && !options.some((p) => p.patientId === value.patientId)
      ? [value, ...options]
      : options;
  return (
    <div className="field">
      <label htmlFor={`${id}-query`}>
        Buscar {label.toLowerCase()} por nombre o DNI
      </label>
      <input
        type="search"
        id={`${id}-query`}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        maxLength={120}
      />
      <label htmlFor={id}>{label}</label>
      <select
        id={id}
        value={value?.patientId ?? ""}
        onChange={(e) =>
          onChange(visible.find((p) => p.patientId === e.target.value) ?? null)
        }
      >
        <option value="">Seleccionar</option>
        {visible.map((p) => (
          <option value={p.patientId} key={p.patientId}>
            {p.fullName} · DNI {p.documentNumber || "sin registrar"}
            {p.enabled ? "" : " · pendiente"}
          </option>
        ))}
      </select>
      {more ? (
        <p className="field__hint">
          Se muestran los primeros 50 resultados. Refiná la búsqueda.
        </p>
      ) : null}
      {error ? <p role="alert">{error}</p> : null}
    </div>
  );
}
