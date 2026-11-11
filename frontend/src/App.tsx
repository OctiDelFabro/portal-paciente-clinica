import { useCallback, useEffect, useState } from "react";
import { configureHttp } from "./api/http";
import { OperationsScreen } from "./features/operations/OperationsScreen";
import { AuthScreen } from "./features/auth/AuthScreen";
import { AppointmentsBoard } from "./features/appointments/AppointmentsBoard";
import { AgendaScreen } from "./features/doctor/AgendaScreen";
import { HistoryScreen } from "./features/history/HistoryScreen";
import { SearchScreen } from "./features/search/SearchScreen";
import { loadSession, saveSession, type Session } from "./session";
import { Rail, type View } from "./ui/Rail";
import {
  PatientProvider,
  PatientPicker,
  usePatientTarget,
} from "./features/patients/PatientContext";
import { NotificationsScreen } from "./features/notifications/NotificationsScreen";

const PATIENT_VIEWS: View[] = [
  "search",
  "appointments",
  "history",
  "notifications",
];

function viewFromHash(session: Session): View {
  const raw = window.location.hash.replace(/^#\/?/, "") as View;
  if (session.role === "doctor") return "agenda";
  if (session.role === "admin" || session.role === "reception")
    return "operations";
  return PATIENT_VIEWS.includes(raw) ? raw : "search";
}

export function App() {
  const [session, setSession] = useState<Session | null>(() => loadSession());
  const [view, setView] = useState<View>(() =>
    session ? viewFromHash(session) : "search",
  );

  const signOut = useCallback(() => {
    saveSession(null);
    setSession(null);
    window.location.hash = "";
  }, []);

  configureHttp(() => session?.token ?? null, signOut);

  useEffect(() => {
    if (!session) return;
    const onHash = () => setView(viewFromHash(session));
    window.addEventListener("hashchange", onHash);
    const expiry = window.setTimeout(
      signOut,
      Math.max(0, session.expiresAt - Date.now()),
    );
    return () => {
      window.removeEventListener("hashchange", onHash);
      window.clearTimeout(expiry);
    };
  }, [session, signOut]);

  if (!session) {
    return (
      <AuthScreen
        onSignedIn={(s) => {
          saveSession(s);
          setSession(s);
          const initial =
            s.role === "doctor"
              ? "agenda"
              : s.role === "patient"
                ? "search"
                : "operations";
          window.location.hash = `#/${initial}`;
          setView(initial);
        }}
      />
    );
  }

  const navigate = (next: View) => {
    window.location.hash = `#/${next}`;
    setView(next);
  };

  const shell = (
    <div className="shell">
      <a className="skip-link" href="#content">
        Saltar al contenido
      </a>
      <Rail
        role={session.role}
        current={view}
        fullName={session.fullName}
        onNavigate={navigate}
        onLogout={signOut}
      />
      <main id="content" className="content" tabIndex={-1}>
        <PatientPicker />
        <Screens session={session} view={view} />
      </main>
    </div>
  );
  return session.role === "patient" ? (
    <PatientProvider session={session}>{shell}</PatientProvider>
  ) : (
    shell
  );
}
function Screens({ session, view }: { session: Session; view: View }) {
  const target = usePatientTarget();
  const key = target?.patientId ?? session.subject;
  return (
    <>
      {view === "search" ? (
        <SearchScreen key={key} session={session} onBooked={() => undefined} />
      ) : null}
      {view === "appointments" ? (
        <AppointmentsBoard key={key} session={session} />
      ) : null}
      {view === "history" ? <HistoryScreen key={key} /> : null}
      {view === "agenda" ? <AgendaScreen doctorId={session.subject} /> : null}
      {view === "operations" ? <OperationsScreen session={session} /> : null}
      {view === "notifications" ? <NotificationsScreen key={key} /> : null}
    </>
  );
}
