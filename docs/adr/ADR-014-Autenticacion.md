# ADR-014 — Autenticación del portal
**Decisión D14 · Estado: aceptada e implementada en el gateway para la presentación final.** Pacientes emite un JWT HS256 al registrarse o iniciar sesión; el gateway lo valida en cada solicitud `/api/*` y reenvía la identidad a los servicios. Registra el supuesto A1 de `odd/tasks/entrega-final.md`.

## Contexto
El enunciado pide un punto de entrada único "con autenticación, direccionamiento y control de tráfico". Hasta la Entrega 2 la ruta `/api/turnos` no tenía autenticación y por eso solo existía en el listener interno (ADR-008). El portal necesita saber qué paciente opera para que Turnos e Historia clínica apliquen sus reglas de propiedad (RF-10). La cátedra no asignó un proveedor de identidad, y el plazo de la presentación final es corto.

## Decisión
| Aspecto | Decisión |
|---|---|
| Emisor | Pacientes (`POST /auth/register`, `POST /auth/login`), dueño de las credenciales (contraseña con bcrypt). |
| Formato | JWT firmado con HS256 y secreto compartido `JWT_SECRET` (al menos 32 bytes; el gateway no arranca con uno más corto). Claims: `sub` (id del paciente), `role`, `iss="pacientes"`, `iat`, `exp`. |
| Validación | El gateway acepta solo HS256 (algoritmo fijado; `none` y otros se rechazan), exige `iss="pacientes"`, `exp` vigente, `sub` y `role` presentes y con caracteres seguros (`[A-Za-z0-9._:@-]`, hasta 128). |
| Propagación | Con un token válido el gateway envía `X-Patient-Id: <sub>` y `X-User-Role: <role>`. Esos headers **siempre** se borran de la solicitud del cliente, en todas las rutas de ambos listeners: un servicio solo los recibe escritos por el gateway. |
| Rutas sin token | `POST /api/pacientes/auth/register`, `POST /api/pacientes/auth/login` y los `GET /api/medicos/*` (catálogo). En ellas un token inválido o vencido se ignora: un token viejo en el navegador no impide volver a entrar. |
| Rutas con token | El resto de `/api/pacientes/*`, las escrituras de `/api/medicos/*`, `/api/turnos/*` y `/api/historia/*`. Sin token válido: 401 JSON `UNAUTHORIZED` con `WWW-Authenticate: Bearer`. |
| Control de tráfico | Token bucket por `sub`, o por IP si no hay token válido, en `/api/*` (`GATEWAY_RATE_LIMIT_RPS` = 10, `GATEWAY_RATE_LIMIT_BURST` = 20). El límite se evalúa antes del 401, así una ráfaga de tokens inválidos también se frena por IP. Exceso: 429 JSON `RATE_LIMITED` con `Retry-After`. |
| Partners | `/v1/*` no usa JWT: sigue con `X-API-Key` validada en Turnos y Notificaciones (ADR-008). El listener público no expone `/api/*`. |
| Navegador | CORS solo en el listener interno, para `CORS_ALLOWED_ORIGINS` (por defecto el servidor de desarrollo de Vite). El token viaja en `Authorization`, no en cookies, así que no hay credenciales CORS ni riesgo de CSRF. |

## Alternativas consideradas
- **Keycloak u otro IdP propio:** estándar (OIDC, RS256, rotación de claves), pero agrega un contenedor pesado, su base y su configuración de realms para un TP con un solo tipo de usuario. Queda como evolución si se suman médicos y recepción.
- **Auth0, Cognito u otro servicio administrado:** sin infraestructura propia, pero depende de una cuenta externa y de red durante la demo, y los datos de pacientes quedarían en un tercero.
- **Sesiones con cookie en el gateway:** obligan a guardar estado de sesión (Redis) compartido por las réplicas del gateway y a protegerse de CSRF.
- **Validar el token en cada servicio:** cada servicio tendría la biblioteca y el secreto. Se centraliza en el gateway; los servicios confían en los headers porque solo son alcanzables desde la red interna.
- **RS256 con clave pública:** evitaría que el gateway pueda emitir tokens. Con un solo emisor y un solo verificador en la misma red se prefirió la simplicidad de HS256; el cambio no altera los claims.

## Consecuencias
- El gateway y Pacientes comparten un secreto: quien lo obtenga puede emitir tokens. Se carga por variable de entorno y no se versiona.
- No hay revocación: un token sigue válido hasta su `exp`. Se mitiga con vencimientos cortos definidos por Pacientes.
- Los servicios deben estar solo en la red interna; si un servicio quedara expuesto, cualquiera podría enviar `X-Patient-Id`.
- El límite de tráfico vive en memoria de cada instancia del gateway, igual que el de los partners (ADR-008).
- La IP de cliente es la del par TCP; no se confía en `X-Forwarded-For`. Detrás de un proxy, los anónimos compartirían un balde; los autenticados no, porque usan `sub`.
- Evidencia: `gateway/cmd/auth_test.go` (alg `none`, HS512, secreto incorrecto, vencido, sin `exp`, `iss` incorrecto, claims faltantes o inseguros, borrado de headers en ambos listeners, política por ruta) y `gateway/cmd/ratelimit_test.go`. Métricas: `gateway_rate_limited_total{route}` y los 401/429 en `http_requests_total{status}`.
