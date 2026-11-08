import type { ReactNode } from 'react';
import type { AppointmentStatus, Price } from '../api/types';
import { folio, formatArs, formatDay, formatKwh, formatTime } from '../format';
import { StatusTag } from './StatusTag';

interface PassProps {
  appointmentId?: string;
  doctorName: string;
  specialties: string[];
  startAt: string;
  endAt: string;
  status?: AppointmentStatus;
  price?: Price;
  priceLoading?: boolean;
  label: string;
  children?: ReactNode;
}

/**
 * The appointment as a travel document: a header strip with the folio, the
 * doctor as the "destination", labeled segments with tabular figures, a
 * perforation, and the fare stub with the energy line from the provider.
 */
export function Pass({ appointmentId, doctorName, specialties, startAt, endAt, status, price, priceLoading, label, children }: PassProps) {
  const minutes = Math.round((new Date(endAt).getTime() - new Date(startAt).getTime()) / 60000);
  return (
    <article className={`pass${status === 'cancelled' ? ' pass--void' : ''}`} aria-label={label}>
      <header className="pass__head">
        <span className="pass__issuer">Clínica Arroyo Claro</span>
        <span className={`pass__folio${appointmentId ? '' : ' pass__folio--pending'}`}>
          {appointmentId ? `N° ${folio(appointmentId)}` : 'Provisorio'}
        </span>
      </header>

      <div className="pass__route">
        <div>
          <p className="pass__code">{formatTime(startAt)}</p>
          <p className="pass__sub">{formatDay(startAt)}</p>
        </div>
        <div className="pass__dest">
          <p className="pass__doctor">{doctorName}</p>
          <p className="pass__sub">{specialties.join(' · ') || 'Consulta'}</p>
        </div>
      </div>

      <dl className="pass__segments">
        <div>
          <dt>Fin</dt>
          <dd>{formatTime(endAt)}</dd>
        </div>
        <div>
          <dt>Duración</dt>
          <dd>{minutes} min</dd>
        </div>
        <div>
          <dt>Estado</dt>
          <dd>{status ? <StatusTag status={status} /> : <span className="tag tag--alert">Sin confirmar</span>}</dd>
        </div>
      </dl>

      <div className="pass__perforation" aria-hidden="true" />

      <FareStub price={price} loading={priceLoading} />

      {children ? <div className="pass__actions">{children}</div> : null}
    </article>
  );
}

function FareStub({ price, loading }: { price?: Price; loading?: boolean }) {
  if (loading) {
    return (
      <div className="fare" aria-busy="true">
        <p className="label">Total de la consulta</p>
        <p className="fare__total fare__total--loading">Calculando…</p>
      </div>
    );
  }
  if (!price) {
    return (
      <div className="fare">
        <p className="label">Total de la consulta</p>
        <p className="fare__note">Sin desglose disponible para este turno.</p>
      </div>
    );
  }
  const pending = price.energy.status !== 'estimated';
  return (
    <div className="fare">
      <p className="label">Total de la consulta</p>
      <p className="fare__total">{formatArs(price.totalArs)}</p>
      <dl className="fare__lines">
        <div>
          <dt>Consulta</dt>
          <dd>{formatArs(price.basePriceArs)}</dd>
        </div>
        <div className={pending ? 'fare__line--pending' : undefined}>
          <dt>
            Energía del consultorio
            {!pending ? <span className="fare__kwh"> · {formatKwh(price.energy.kWh)}</span> : null}
          </dt>
          <dd>{pending ? <span className="tag tag--alert">Pendiente</span> : formatArs(price.energy.costArs)}</dd>
        </div>
      </dl>
      {pending ? (
        <p className="fare__note">
          El proveedor de energía no respondió. Tu turno vale igual; el costo energético se calcula apenas el servicio vuelva.
        </p>
      ) : null}
    </div>
  );
}
