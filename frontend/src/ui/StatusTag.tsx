import type { AppointmentStatus, EnergyCost } from '../api/types';

const LABELS: Record<string, string> = { booked: 'Confirmado', cancelled: 'Cancelado' };

export function StatusTag({ status }: { status: AppointmentStatus }) {
  const known = status === 'booked' || status === 'cancelled';
  return <span className={`tag tag--${known ? status : 'other'}`}>{LABELS[status] ?? status}</span>;
}

export function EnergyTag({ energy }: { energy?: EnergyCost }) {
  if (!energy) return <span className="tag tag--muted">Sin dato</span>;
  return energy.status === 'estimated' ? (
    <span className="tag tag--muted">Estimada</span>
  ) : (
    <span className="tag tag--alert">Pendiente</span>
  );
}

export function ChangedTag() {
  return <span className="tag tag--alert tag--pulse">Cambió</span>;
}
