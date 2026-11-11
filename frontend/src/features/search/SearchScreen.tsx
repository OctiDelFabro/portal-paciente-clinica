import { usePatientTarget } from "../patients/PatientContext";
import { useEffect, useState, type FormEvent } from "react";
import { ArrowRight, ChevronLeft, ChevronRight, Search } from "lucide-react";
import { medicos, turnos } from "../../api/endpoints";
import { ApiError } from "../../api/http";
import type {
  Appointment,
  Doctor,
  SearchQuery,
  SearchResult,
  Specialty,
} from "../../api/types";
import {
  formatDay,
  formatTime,
  SPECIALTY_LABELS,
  specialtyName,
} from "../../format";
import type { Session } from "../../session";
import { Notice } from "../../ui/Notice";
import { BookingPanel } from "../booking/BookingPanel";
import { NextPass } from "../appointments/NextPass";

const PAGE_SIZE = 8;
const INITIAL: SearchQuery = {
  q: "",
  specialty: "",
  acceptsMinors: false,
  sort: "nextAvailable",
  page: 1,
  size: PAGE_SIZE,
};

// Turnos owns schedules, so the first free slot comes from it, not from the
// approximate nextAvailableAt that Medicos indexes (ADR-006).
type Next =
  | { kind: "loading" }
  | { kind: "slot"; startAt: string }
  | { kind: "none" }
  | { kind: "error" };

type State =
  | { kind: "loading" }
  | { kind: "ready"; result: SearchResult }
  | { kind: "unavailable"; message: string }
  | { kind: "error"; message: string };

export function SearchScreen({
  session,
  onBooked,
}: {
  session: Session;
  onBooked: (a: Appointment) => void;
}) {
  const target = usePatientTarget();
  const child = !!target && target.patientId !== target.selfId;
  const [revision, setRevision] = useState(0);
  const [query, setQuery] = useState<SearchQuery>(INITIAL);
  const [draft, setDraft] = useState("");
  const [state, setState] = useState<State>({ kind: "loading" });
  const [specialties, setSpecialties] = useState<Specialty[]>([]);
  const [selected, setSelected] = useState<Doctor | null>(null);
  const [next, setNext] = useState<Record<string, Next>>({});

  useEffect(() => {
    medicos
      .specialties()
      .then((r) => {
        r.specialties.forEach((s) => (SPECIALTY_LABELS[s.code] = s.name));
        setSpecialties(r.specialties);
      })
      .catch(() => setSpecialties([]));
  }, []);

  useEffect(() => {
    let alive = true;
    setState({ kind: "loading" });
    async function load() {
      try {
        const result = await medicos.catalog({
          ...query,
          acceptsMinors: query.acceptsMinors || child,
        });
        const values: Record<string, Next> = {};
        let cursor = 0;
        async function worker() {
          while (alive && cursor < result.items.length) {
            const id = result.items[cursor++].doctorId;
            try {
              const r = await turnos.nextAvailable(id);
              values[id] = r.startAt
                ? { kind: "slot", startAt: r.startAt }
                : { kind: "none" };
            } catch {
              values[id] = { kind: "error" };
            }
          }
        }
        await Promise.all(
          Array.from({ length: Math.min(4, result.items.length) }, worker),
        );
        if (alive) {
          setNext(values);
          setState({ kind: "ready", result });
        }
      } catch (err) {
        if (!alive) return;
        const e = err instanceof ApiError ? err : null;
        setState(
          e?.unavailable
            ? { kind: "unavailable", message: e.message }
            : { kind: "error", message: e?.message ?? "No pudimos buscar." },
        );
      }
    }
    void load();
    return () => {
      alive = false;
    };
  }, [query.q, query.specialty, query.acceptsMinors, child, revision]);

  const items = (() => {
    if (state.kind !== "ready") return [];
    if (query.sort !== "nextAvailable")
      return [...state.result.items]
        .sort((a, b) => a.fullName.localeCompare(b.fullName, "es"))
        .slice((query.page - 1) * PAGE_SIZE, query.page * PAGE_SIZE);
    // Sort the whole result by authoritative slots BEFORE paginating; unknown values go last.
    const key = (id: string) => {
      const n = next[id];
      return n?.kind === "slot"
        ? Date.parse(n.startAt)
        : Number.POSITIVE_INFINITY;
    };
    return [...state.result.items]
      .sort(
        (a, b) =>
          key(a.doctorId) - key(b.doctorId) ||
          a.fullName.localeCompare(b.fullName, "es"),
      )
      .slice((query.page - 1) * PAGE_SIZE, query.page * PAGE_SIZE);
  })();

  const update = (patch: Partial<SearchQuery>) =>
    setQuery((q) => ({ ...q, ...patch, page: patch.page ?? 1 }));
  const submit = (e: FormEvent) => {
    e.preventDefault();
    update({ q: draft });
  };

  const total = state.kind === "ready" ? state.result.total : 0;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div className="workspace">
      <section className="workspace__main" aria-labelledby="search-title">
        <header className="screen-head">
          <h1 id="search-title" className="screen-title">
            Buscar médico
          </h1>
          <p className="screen-lede">
            Elegí un profesional y mirá sus horarios libres. Las reservas
            requieren 24 h de anticipación.
          </p>
        </header>

        <form className="toolbar" onSubmit={submit} role="search">
          <div className="field field--grow">
            <label htmlFor="q">Nombre o especialidad</label>
            <div className="input-icon">
              <Search size={18} strokeWidth={2} aria-hidden="true" />
              <input
                id="q"
                type="search"
                value={draft}
                placeholder="Ej.: Gómez, pediatría"
                onChange={(e) => setDraft(e.target.value)}
              />
            </div>
          </div>
          <div className="field">
            <label htmlFor="specialty">Especialidad</label>
            <select
              id="specialty"
              value={query.specialty}
              onChange={(e) => update({ specialty: e.target.value })}
            >
              <option value="">Todas</option>
              {specialties.map((s) => (
                <option key={s.code} value={s.code}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="sort">Ordenar por</label>
            <select
              id="sort"
              value={query.sort}
              onChange={(e) =>
                update({ sort: e.target.value as SearchQuery["sort"] })
              }
            >
              <option value="nextAvailable">Primero disponible</option>
              <option value="name">Nombre (A–Z)</option>
            </select>
          </div>
          <label className="check">
            <input
              type="checkbox"
              checked={query.acceptsMinors || child}
              disabled={child}
              onChange={(e) => update({ acceptsMinors: e.target.checked })}
            />
            Atiende menores
          </label>
          <button className="btn btn--primary" type="submit">
            Buscar
          </button>
        </form>

        {state.kind === "unavailable" ? (
          <Notice
            tone="alert"
            title="La búsqueda no está disponible en este momento"
          >
            El índice de profesionales no responde. Tus turnos y tu historia
            clínica siguen funcionando.
          </Notice>
        ) : null}
        {state.kind === "error" ? (
          <Notice tone="error" title="No pudimos buscar">
            {state.message}
          </Notice>
        ) : null}

        <div
          className="board"
          aria-busy={state.kind === "loading"}
          hidden={state.kind === "unavailable" || state.kind === "error"}
        >
          <table className="board__table">
            <caption className="visually-hidden">
              Resultados de la búsqueda de profesionales
            </caption>
            <thead>
              <tr>
                <th scope="col">Profesional</th>
                <th scope="col">Especialidades</th>
                <th scope="col">Próximo libre</th>
                <th scope="col">
                  <span className="visually-hidden">Acción</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {state.kind === "loading"
                ? Array.from({ length: 4 }, (_, i) => (
                    <tr key={i} className="board__skeleton" aria-hidden="true">
                      <td colSpan={4}>
                        <span />
                      </td>
                    </tr>
                  ))
                : null}
              {state.kind === "ready" && state.result.items.length === 0 ? (
                <tr>
                  <td colSpan={4} className="board__empty">
                    Ningún profesional coincide con esos filtros. Probá con otra
                    especialidad o quitá «Atiende menores».
                  </td>
                </tr>
              ) : null}
              {state.kind === "ready"
                ? items.map((d) => (
                    <tr
                      key={d.doctorId}
                      className={
                        selected?.doctorId === d.doctorId
                          ? "is-selected"
                          : undefined
                      }
                    >
                      <th scope="row">
                        <span className="board__primary">{d.fullName}</span>
                        {d.acceptsMinors ? (
                          <span className="board__meta">Atiende menores</span>
                        ) : null}
                      </th>
                      <td>{d.specialties.map(specialtyName).join(" · ")}</td>
                      <td className="board__date">
                        <NextCell value={next[d.doctorId]} />
                      </td>
                      <td className="board__action">
                        <button
                          type="button"
                          className="btn btn--secondary btn--small"
                          onClick={() => setSelected(d)}
                          aria-label={`Ver disponibilidad de ${d.fullName}`}
                        >
                          Ver disponibilidad{" "}
                          <ArrowRight
                            size={16}
                            strokeWidth={2}
                            aria-hidden="true"
                          />
                        </button>
                      </td>
                    </tr>
                  ))
                : null}
            </tbody>
          </table>
          {state.kind === "ready" && total > 0 ? (
            <div className="pager">
              <span className="pager__count num">
                {total} {total === 1 ? "profesional" : "profesionales"} · página{" "}
                {query.page} de {pages}
              </span>
              <div className="pager__buttons">
                <button
                  type="button"
                  className="btn btn--ghost btn--small"
                  disabled={query.page <= 1}
                  onClick={() => update({ page: query.page - 1 })}
                >
                  <ChevronLeft size={16} aria-hidden="true" /> Anterior
                </button>
                <button
                  type="button"
                  className="btn btn--ghost btn--small"
                  disabled={query.page >= pages}
                  onClick={() => update({ page: query.page + 1 })}
                >
                  Siguiente <ChevronRight size={16} aria-hidden="true" />
                </button>
              </div>
            </div>
          ) : null}
        </div>
      </section>

      <aside
        className="workspace__side"
        aria-label={selected ? "Reserva" : "Próximo turno"}
      >
        {selected ? (
          <BookingPanel
            key={selected.doctorId}
            session={session}
            doctor={selected}
            onClose={() => setSelected(null)}
            patientId={target?.patientId ?? session.subject}
            initialDate={
              next[selected.doctorId]?.kind === "slot"
                ? (
                    next[selected.doctorId] as { kind: "slot"; startAt: string }
                  ).startAt.slice(0, 10)
                : undefined
            }
            onBooked={(a) => {
              setRevision((n) => n + 1);
              onBooked(a);
            }}
          />
        ) : (
          <NextPass />
        )}
      </aside>
    </div>
  );
}

function NextCell({ value }: { value: Next | undefined }) {
  if (!value || value.kind === "loading")
    return <span className="board__meta">Consultando…</span>;
  if (value.kind === "none")
    return (
      <span className="board__meta">Sin turnos en los próximos 60 días</span>
    );
  if (value.kind === "error") return <>—</>;
  return (
    <>
      {formatDay(value.startAt)}{" "}
      <span className="board__time">{formatTime(value.startAt)}</span>
    </>
  );
}
