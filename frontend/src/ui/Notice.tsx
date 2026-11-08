import type { ReactNode } from 'react';
import { CircleAlert, TriangleAlert } from 'lucide-react';

type Tone = 'alert' | 'error';

/** A full-width status line: yellow for a degraded-but-working state, red for a failure. */
export function Notice({ tone, title, children, action }: { tone: Tone; title: string; children?: ReactNode; action?: ReactNode }) {
  const Icon = tone === 'error' ? CircleAlert : TriangleAlert;
  return (
    <div className={`notice notice--${tone}`} role={tone === 'error' ? 'alert' : 'status'}>
      <span className="notice__icon" aria-hidden="true">
        <Icon size={20} strokeWidth={2} />
      </span>
      <div className="notice__body">
        <p className="notice__title">{title}</p>
        {children ? <p className="notice__text">{children}</p> : null}
      </div>
      {action ? <div className="notice__action">{action}</div> : null}
    </div>
  );
}
