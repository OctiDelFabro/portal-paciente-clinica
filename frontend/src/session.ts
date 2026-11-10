import type { Role } from './api/types';

// The access token lives in sessionStorage: it survives a reload during the
// demo but not a closed tab. The browser never verifies the signature; the
// gateway does. Claims are read only to label the session and expire it.

export interface Session {
  token: string;
  subject: string;
  role: Role;
  fullName: string;
  expiresAt: number;
}

const KEY = 'arroyo-claro.session';

export function decodeSession(token: string, fullName: string): Session | null {
  const [, payload] = token.split('.');
  if (!payload) return null;
  try {
    const json = JSON.parse(atob(payload.replace(/-/g, '+').replace(/_/g, '/'))) as {
      sub?: string;
      role?: string;
      exp?: number;
    };
    if (!json.sub || !json.exp) return null;
    if (!['patient', 'doctor', 'admin', 'reception'].includes(json.role ?? '')) return null;
    const role = json.role as Role;
    return { token, subject: json.sub, role, fullName, expiresAt: json.exp * 1000 };
  } catch {
    return null;
  }
}

export function loadSession(): Session | null {
  try {
    const raw = window.sessionStorage.getItem(KEY);
    if (!raw) return null;
    const session = JSON.parse(raw) as Session;
    return session.expiresAt > Date.now() && ['patient', 'doctor', 'admin', 'reception'].includes(session.role) ? session : null;
  } catch {
    return null;
  }
}

export function saveSession(session: Session | null): void {
  try {
    if (session) window.sessionStorage.setItem(KEY, JSON.stringify(session));
    else window.sessionStorage.removeItem(KEY);
  } catch {
    // Storage blocked (private mode): the session lives only in memory.
  }
}
