import { CalendarClock, ClipboardList, FileHeart, LogOut, Search } from 'lucide-react';
import type { Role } from '../api/types';

export type View = 'search' | 'appointments' | 'history' | 'agenda';

const ITEMS: Record<Role, { view: View; label: string; Icon: typeof Search }[]> = {
  patient: [
    { view: 'search', label: 'Buscar médico', Icon: Search },
    { view: 'appointments', label: 'Mis turnos', Icon: CalendarClock },
    { view: 'history', label: 'Historia clínica', Icon: FileHeart },
  ],
  doctor: [{ view: 'agenda', label: 'Mi agenda', Icon: ClipboardList }],
};

interface RailProps {
  role: Role;
  current: View;
  fullName: string;
  onNavigate: (view: View) => void;
  onLogout: () => void;
}

export function Rail({ role, current, fullName, onNavigate, onLogout }: RailProps) {
  return (
    <aside className="rail">
      <div className="rail__brand">
        <span className="rail__mark" aria-hidden="true">
          AC
        </span>
        <span>
          <span className="rail__name">Arroyo Claro</span>
          <span className="rail__tagline">Portal del paciente</span>
        </span>
      </div>

      <nav aria-label="Secciones">
        <ul className="rail__nav">
          {ITEMS[role].map(({ view, label, Icon }) => (
            <li key={view}>
              <a
                href={`#/${view}`}
                className="rail__link"
                aria-current={current === view ? 'page' : undefined}
                onClick={(e) => {
                  e.preventDefault();
                  onNavigate(view);
                }}
              >
                <Icon size={18} strokeWidth={2} aria-hidden="true" />
                {label}
              </a>
            </li>
          ))}
        </ul>
      </nav>

      <div className="rail__session">
        <span className="label label--inverse">{role === 'doctor' ? 'Profesional' : 'Paciente'}</span>
        <span className="rail__user">{fullName}</span>
        <button type="button" className="rail__logout" onClick={onLogout}>
          <LogOut size={16} strokeWidth={2} aria-hidden="true" />
          Salir
        </button>
      </div>
    </aside>
  );
}
