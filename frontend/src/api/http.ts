// Thin fetch wrapper for the gateway. Every service answers errors with
// {"error":{"code","message"}}; anything else (proxy pages, timeouts) is
// normalized so screens never treat an infrastructure reply as success.

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly retryAfterSeconds?: number,
  ) {
    super(message);
    this.name = "ApiError";
  }

  /** The dependency behind this call is down or slow (gateway 502/503/504). */
  get unavailable(): boolean {
    return (
      this.status === 0 ||
      this.status === 502 ||
      this.status === 503 ||
      this.status === 504
    );
  }
}

type TokenSource = () => string | null;
type UnauthorizedHandler = () => void;

let tokenSource: TokenSource = () => null;
let onUnauthorized: UnauthorizedHandler = () => {};

export function configureHttp(
  source: TokenSource,
  unauthorized: UnauthorizedHandler,
): void {
  tokenSource = source;
  onUnauthorized = unauthorized;
}

const TIMEOUT_MS = 8000;

export async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  timeoutMs = TIMEOUT_MS,
): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const token = tokenSource();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal,
    });
    const isJson = (response.headers.get("Content-Type") ?? "").includes(
      "application/json",
    );
    let payload: unknown = null;
    if (isJson && response.status !== 204) {
      try {
        payload = await response.json();
      } catch (err) {
        if (controller.signal.aborted) throw err;
      }
    }
    if (controller.signal.aborted) throw new Error("timeout");
    if (response.ok) {
      if (response.status === 204) return undefined as T;
      if (!isJson || payload === null)
        throw new ApiError(
          502,
          "BAD_RESPONSE",
          "Recibimos una respuesta inesperada. Intentá de nuevo en unos segundos.",
        );
      return payload as T;
    }
    const err = (
      payload as { error?: { code?: string; message?: string } } | null
    )?.error;
    const value = Number.parseInt(
      response.headers.get("Retry-After") ?? "",
      10,
    );
    // A late response from an old session must not sign out a newer one.
    if (response.status === 401 && token && tokenSource() === token)
      onUnauthorized();
    throw new ApiError(
      response.status,
      err?.code ?? `HTTP_${response.status}`,
      err?.message ?? defaultMessage(response.status),
      Number.isFinite(value) ? value : undefined,
    );
  } catch (err) {
    if (err instanceof ApiError) throw err;
    throw new ApiError(
      0,
      controller.signal.aborted ? "TIMEOUT" : "NETWORK",
      "No pudimos confirmar la respuesta del portal. Revisá el estado antes de repetir una operación.",
    );
  } finally {
    window.clearTimeout(timer);
  }
}

function defaultMessage(status: number): string {
  if (status === 401) return "Tu sesión venció. Volvé a ingresar.";
  if (status === 403) return "No tenés permiso para hacer esto.";
  if (status === 404) return "No encontramos lo que buscabas.";
  if (status === 429)
    return "Hiciste muchas solicitudes seguidas. Esperá unos segundos.";
  if (status >= 500) return "El servicio no está disponible en este momento.";
  return "No pudimos completar la operación.";
}
