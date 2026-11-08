import { useCallback, useEffect, useRef, useState } from 'react';
import { turnos } from '../../api/endpoints';
import { ApiError } from '../../api/http';
import type { Appointment } from '../../api/types';

const POLL_MS = 15000;
const SEEN_KEY = 'arroyo-claro.seen';

export type LoadState = { kind: 'loading' } | { kind: 'ready' } | { kind: 'error'; message: string; unavailable: boolean };

/** Fingerprint of what the patient should notice when it changes. */
const fingerprint = (a: Appointment) => `${a.status}|${a.price?.energy.status ?? '-'}|${a.startAt}`;

function readSeen(): Record<string, string> {
  try {
    return JSON.parse(window.sessionStorage.getItem(SEEN_KEY) ?? '{}') as Record<string, string>;
  } catch {
    return {};
  }
}

function writeSeen(seen: Record<string, string>) {
  try {
    window.sessionStorage.setItem(SEEN_KEY, JSON.stringify(seen));
  } catch {
    // Without storage, changes are tracked for this page view only.
  }
}

/**
 * Live list of the caller's appointments, ranked by start time and polled.
 * A row whose status or energy estimate changed since it was last seen is
 * flagged until the patient acknowledges it.
 */
export function useAppointments() {
  const [items, setItems] = useState<Appointment[]>([]);
  const [state, setState] = useState<LoadState>({ kind: 'loading' });
  const [changed, setChanged] = useState<Set<string>>(new Set());
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);
  const seen = useRef<Record<string, string>>(readSeen());

  const refresh = useCallback(async () => {
    try {
      const list = await turnos.list();
      const ranked = [...list].sort((a, b) => a.startAt.localeCompare(b.startAt));
      const next = new Set<string>();
      for (const a of ranked) {
        const prev = seen.current[a.id];
        if (prev === undefined) seen.current[a.id] = fingerprint(a);
        else if (prev !== fingerprint(a)) next.add(a.id);
      }
      writeSeen(seen.current);
      setItems(ranked);
      setChanged(next);
      setUpdatedAt(new Date());
      setState({ kind: 'ready' });
    } catch (err) {
      const e = err instanceof ApiError ? err : null;
      setState({ kind: 'error', message: e?.message ?? 'No pudimos traer tus turnos.', unavailable: e?.unavailable ?? false });
    }
  }, []);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), POLL_MS);
    return () => window.clearInterval(timer);
  }, [refresh]);

  const acknowledge = useCallback(
    (id: string) => {
      const a = items.find((x) => x.id === id);
      if (!a) return;
      seen.current[id] = fingerprint(a);
      writeSeen(seen.current);
      setChanged((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
    },
    [items],
  );

  return { items, state, changed, updatedAt, refresh, acknowledge };
}
