import { useEffect, useState } from 'react';
import { medicos } from '../api/endpoints';
import type { Doctor } from '../api/types';

// Appointments carry only a doctorId; profiles come from Médicos (MySQL,
// works even when the search index is down). Cached for the page's life.
const cache = new Map<string, Promise<Doctor | null>>();

export function loadDoctor(id: string): Promise<Doctor | null> {
  let hit = cache.get(id);
  if (!hit) {
    hit = medicos.doctor(id).catch(() => {
      cache.delete(id);
      return null;
    });
    cache.set(id, hit);
  }
  return hit;
}

export function useDoctors(ids: string[]): Record<string, Doctor | null> {
  const [byId, setById] = useState<Record<string, Doctor | null>>({});
  const key = [...new Set(ids)].sort().join(',');
  useEffect(() => {
    let alive = true;
    const unique = key ? key.split(',') : [];
    Promise.all(unique.map(async (id) => [id, await loadDoctor(id)] as const)).then((pairs) => {
      if (alive) setById(Object.fromEntries(pairs));
    });
    return () => {
      alive = false;
    };
  }, [key]);
  return byId;
}

export const doctorLabel = (doctor: Doctor | null | undefined, fallbackId: string) => doctor?.fullName ?? fallbackId;
