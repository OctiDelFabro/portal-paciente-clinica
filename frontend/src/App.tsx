import { useCallback, useEffect, useState } from 'react';
import { configureHttp } from './api/http';
import { AuthScreen } from './features/auth/AuthScreen';
import { AppointmentsBoard } from './features/appointments/AppointmentsBoard';
import { AgendaScreen } from './features/doctor/AgendaScreen';
import { HistoryScreen } from './features/history/HistoryScreen';
import { SearchScreen } from './features/search/SearchScreen';
import { loadSession, saveSession, type Session } from './session';
import { Rail, type View } from './ui/Rail';

const PATIENT_VIEWS: View[] = ['search', 'appointments', 'history'];

function viewFromHash(session: Session): View {
  const raw = window.location.hash.replace(/^#\/?/, '') as View;
  if (session.role === 'doctor') return 'agenda';
  return PATIENT_VIEWS.includes(raw) ? raw : 'search';
}

export function App() {
  const [session, setSession] = useState<Session | null>(() => loadSession());
  const [view, setView] = useState<View>(() => (session ? viewFromHash(session) : 'search'));

  const signOut = useCallback(() => {
    saveSession(null);
    setSession(null);
    window.location.hash = '';
  }, []);

  configureHttp(() => session?.token ?? null, signOut);

  useEffect(() => {
    if (!session) return;
    const onHash = () => setView(viewFromHash(session));
    window.addEventListener('hashchange', onHash);
    const expiry = window.setTimeout(signOut, Math.max(0, session.expiresAt - Date.now()));
    return () => {
      window.removeEventListener('hashchange', onHash);
      window.clearTimeout(expiry);
    };
  }, [session, signOut]);

  if (!session) {
    return (
      <AuthScreen
        onSignedIn={(s) => {
          saveSession(s);
          setSession(s);
          window.location.hash = s.role === 'doctor' ? '#/agenda' : '#/search';
          setView(s.role === 'doctor' ? 'agenda' : 'search');
        }}
      />
    );
  }

  const navigate = (next: View) => {
    window.location.hash = `#/${next}`;
    setView(next);
  };

  return (
    <div className="shell">
      <a className="skip-link" href="#content">
        Saltar al contenido
      </a>
      <Rail role={session.role} current={view} fullName={session.fullName} onNavigate={navigate} onLogout={signOut} />
      <main id="content" className="content" tabIndex={-1}>
        {view === 'search' ? <SearchScreen session={session} onBooked={() => undefined} /> : null}
        {view === 'appointments' ? <AppointmentsBoard /> : null}
        {view === 'history' ? <HistoryScreen /> : null}
        {view === 'agenda' ? <AgendaScreen /> : null}
      </main>
    </div>
  );
}
