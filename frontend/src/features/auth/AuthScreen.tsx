import { useState, type FormEvent } from 'react';
import { pacientes } from '../../api/endpoints';
import { ApiError } from '../../api/http';
import { decodeSession, type Session } from '../../session';
import { Notice } from '../../ui/Notice';

type Mode = 'login' | 'register';

export function AuthScreen({ onSignedIn }: { onSignedIn: (s: Session) => void }) {
  const [mode, setMode] = useState<Mode>('login');
  const [registered, setRegistered] = useState(false);

  return (
    <main className="auth">
      <section className="auth__intro" aria-labelledby="auth-title">
        <p className="auth__brand">Clínica Arroyo Claro</p>
        <h1 id="auth-title" className="auth__title">
          Tu turno,
          <br />
          en un pase.
        </h1>
        <p className="auth__lede">
          Buscá profesionales, elegí un horario y llevate el pase con el total de la consulta. Tus turnos se ordenan solos y te
          avisan cuando algo cambia.
        </p>
        <ul className="auth__facts">
          <li>
            <span className="label label--inverse">Anticipación</span>
            <span className="auth__fact">24 h</span>
          </li>
          <li>
            <span className="label label--inverse">Cancelación</span>
            <span className="auth__fact">Hasta 24 h antes</span>
          </li>
        </ul>
        <p className="auth__fine">Clínica ficticia creada para el trabajo práctico de Arquitectura de Software 2026.</p>
      </section>

      <section className="auth__panel" aria-labelledby="auth-panel-title">
        <h2 id="auth-panel-title" className="auth__heading">
          {mode === 'login' ? 'Ingresá a tu cuenta' : 'Creá tu cuenta'}
        </h2>
        <div className="segmented" role="tablist" aria-label="Acceso">
          <button type="button" role="tab" aria-selected={mode === 'login'} onClick={() => setMode('login')}>
            Ingresar
          </button>
          <button type="button" role="tab" aria-selected={mode === 'register'} onClick={() => setMode('register')}>
            Crear cuenta
          </button>
        </div>

        {registered && mode === 'login' ? (
          <Notice tone="alert" title="Cuenta creada, pendiente de verificación">
            Recepción tiene que verificar tus datos antes de que puedas reservar. Ya podés ingresar y buscar profesionales.
          </Notice>
        ) : null}

        {mode === 'login' ? (
          <LoginForm onSignedIn={onSignedIn} />
        ) : (
          <RegisterForm
            onRegistered={() => {
              setRegistered(true);
              setMode('login');
            }}
          />
        )}
      </section>
    </main>
  );
}

function LoginForm({ onSignedIn }: { onSignedIn: (s: Session) => void }) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await pacientes.login(email.trim(), password);
      const name = res.user?.fullName ?? res.patient?.fullName ?? email;
      const session = decodeSession(res.accessToken, name);
      if (!session) throw new ApiError(502, 'BAD_TOKEN', 'El servidor devolvió una sesión inválida.');
      onSignedIn(session);
    } catch (err) {
      setError(loginMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="form" onSubmit={submit} noValidate>
      <div className="field">
        <label htmlFor="login-email">Correo electrónico</label>
        <input id="login-email" type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />
      </div>
      <div className="field">
        <label htmlFor="login-password">Contraseña</label>
        <input
          id="login-password"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </div>
      {error ? <p className="form__error" role="alert">{error}</p> : null}
      <button className="btn btn--primary btn--block" type="submit" disabled={busy || !email || !password}>
        {busy ? 'Ingresando…' : 'Ingresar'}
      </button>
    </form>
  );
}

function loginMessage(err: unknown): string {
  if (!(err instanceof ApiError)) return 'No pudimos iniciar sesión.';
  if (err.status === 401) return 'El correo o la contraseña no coinciden.';
  if (err.status === 429) {
    const wait = err.retryAfterSeconds ? ` Probá de nuevo en ${Math.ceil(err.retryAfterSeconds / 60)} min.` : '';
    return `Demasiados intentos fallidos para esta cuenta.${wait}`;
  }
  if (err.unavailable) return 'El servicio de ingreso no responde. Intentá en unos segundos.';
  return err.message;
}

function RegisterForm({ onRegistered }: { onRegistered: () => void }) {
  const [form, setForm] = useState({ fullName: '', documentNumber: '', birthDate: '', email: '', password: '' });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const set = (key: keyof typeof form) => (e: { target: { value: string } }) => setForm((f) => ({ ...f, [key]: e.target.value }));
  const shortPassword = form.password.length > 0 && form.password.length < 8;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await pacientes.register({ ...form, email: form.email.trim(), fullName: form.fullName.trim() });
      onRegistered();
    } catch (err) {
      if (err instanceof ApiError && err.code === 'EMAIL_TAKEN') setError('Ya existe una cuenta con ese correo. Ingresá con tu contraseña.');
      else if (err instanceof ApiError && err.unavailable) setError('El registro no responde ahora. Intentá en unos segundos.');
      else setError(err instanceof ApiError ? err.message : 'No pudimos crear la cuenta.');
    } finally {
      setBusy(false);
    }
  }

  const complete = Object.values(form).every((v) => v.trim() !== '') && !shortPassword;

  return (
    <form className="form" onSubmit={submit} noValidate>
      <div className="field">
        <label htmlFor="reg-name">Nombre y apellido</label>
        <input id="reg-name" autoComplete="name" required value={form.fullName} onChange={set('fullName')} />
      </div>
      <div className="form__row">
        <div className="field">
          <label htmlFor="reg-dni">DNI</label>
          <input id="reg-dni" inputMode="numeric" required value={form.documentNumber} onChange={set('documentNumber')} />
        </div>
        <div className="field">
          <label htmlFor="reg-birth">Fecha de nacimiento</label>
          <input id="reg-birth" type="date" required value={form.birthDate} onChange={set('birthDate')} />
        </div>
      </div>
      <div className="field">
        <label htmlFor="reg-email">Correo electrónico</label>
        <input id="reg-email" type="email" autoComplete="email" required value={form.email} onChange={set('email')} />
      </div>
      <div className="field">
        <label htmlFor="reg-password">Contraseña</label>
        <input
          id="reg-password"
          type="password"
          autoComplete="new-password"
          required
          minLength={8}
          aria-describedby="reg-password-hint"
          aria-invalid={shortPassword || undefined}
          value={form.password}
          onChange={set('password')}
        />
        <p id="reg-password-hint" className={`field__hint${shortPassword ? ' field__hint--error' : ''}`}>
          Mínimo 8 caracteres.
        </p>
      </div>
      {error ? <p className="form__error" role="alert">{error}</p> : null}
      <button className="btn btn--primary btn--block" type="submit" disabled={busy || !complete}>
        {busy ? 'Creando cuenta…' : 'Crear cuenta'}
      </button>
    </form>
  );
}
