"""Login del dashboard local de Asterion Core.

Ya NO usa Google/Firebase — antes verificaba un ID token de Google contra
los certificados públicos de Firebase; hoy el acceso lo controla un token
propio que genera `asterion local serve` (el CLI, Go) la primera vez que
corre, guardado (hasheado con SHA-256, nunca en texto plano) en
~/.config/asterion/local-auth.yaml con permisos 0600 — ver
internal/localauth en asterion-core. Ese mismo archivo es la única fuente
de verdad: backend-core solo lo lee y compara, nunca genera ni rota el
token por su cuenta (eso es del CLI: `asterion local auth rotate`).

Esto elimina el requisito de tener un proyecto de Firebase solo para usar
el dashboard 100% local — cero dependencia externa para autenticarse.
"""

import hashlib
import ipaddress
import secrets
import time
from collections import defaultdict, deque

import yaml
from fastapi import Cookie, HTTPException, status

from app.config import CLI_CONFIG_DIR, get_settings

SESSION_COOKIE = "asterion_local_session"
SESSION_TTL_SECONDS = 12 * 3600
AUTH_FILE = CLI_CONFIG_DIR / "local-auth.yaml"

# Sesión en memoria, no en disco: este proceso es de un solo usuario y vive
# mientras corre `asterion local serve` — no hay nada que persistir entre
# reinicios más allá del propio token (que ya vive en local-auth.yaml).
_sessions: dict[str, dict] = {}


class NoTokenConfigured(Exception):
    """No existe local-auth.yaml todavía — hay que correr `asterion local
    serve` (o `asterion local auth rotate`) al menos una vez primero."""


class InvalidToken(Exception):
    pass


class IPNotAllowed(Exception):
    """La IP del cliente no está en LOGIN_ALLOWED_IPS."""


class RateLimitExceeded(Exception):
    """Se pasó del límite de intentos — retry_after son los segundos que
    faltan para poder reintentar."""

    def __init__(self, retry_after: int):
        self.retry_after = retry_after
        super().__init__(f"rate limit excedido, reintentar en {retry_after}s")


# --- Rate limit de POST /api/auth/token -------------------------------
#
# Todo en memoria a propósito (mismo criterio que _sessions arriba): esto
# es un proceso de un solo usuario, reiniciarlo ya requiere acceso a la
# máquina, así que perder el historial de intentos en un restart no es una
# ventana de ataque real.
#
# Tres capas, pensadas para el caso concreto que motivó esto — alguien
# probando token tras token, y si lo bloqueás por IP, cambiando de IP para
# seguir probando:
#   1. Lista blanca de IPs (opcional, ver Settings.login_allowed_ips).
#   2. Límite por IP: N intentos fallidos por ventana (default 5 / 5min),
#      con un bloqueo que se DUPLICA cada vez que la misma IP vuelve a
#      pasarse del límite después de que el bloqueo anterior ya expiró —
#      así insistir sale cada vez más caro, no siempre la misma espera.
#   3. Límite global: cuenta los intentos fallidos de TODAS las IPs juntas
#      en la misma ventana — a alguien rotando de IP el límite por-IP no le
#      hace nada (nunca junta 5 intentos con la misma IP), pero el volumen
#      total sigue subiendo igual, y este límite lo agarra a él.
_failed_by_ip: dict[str, deque[float]] = defaultdict(deque)
_strikes_by_ip: dict[str, int] = defaultdict(int)
_locked_until_by_ip: dict[str, float] = {}
_global_failed: deque[float] = deque()
_global_locked_until: float = 0.0

_MAX_STRIKES_FOR_BACKOFF = 10  # 2**10 * 300s ya excede el techo de 24h — no hace falta recursar más profundo


def _prune(attempts: deque[float], window_start: float) -> None:
    while attempts and attempts[0] < window_start:
        attempts.popleft()


def _lockout_seconds(strikes: int, base_window: int) -> int:
    """Cuánto dura el próximo bloqueo de una IP que ya gatilló el límite
    `strikes` veces. Recursiva a propósito: cada strike duplica el
    bloqueo anterior (tope 24h), así que alguien que espera pacientemente
    a que se levante el bloqueo para reintentar encuentra una espera cada
    vez más larga, no siempre la misma ventana de 5 minutos."""
    strikes = min(strikes, _MAX_STRIKES_FOR_BACKOFF)
    if strikes <= 1:
        return base_window
    return min(2 * _lockout_seconds(strikes - 1, base_window), 24 * 3600)


def check_ip_allowed(ip: str) -> None:
    """No hace nada si LOGIN_ALLOWED_IPS está vacío (default) — solo
    empieza a restringir en cuanto se configura al menos una entrada."""
    settings = get_settings()
    if not settings.login_allowed_ips:
        return
    try:
        addr = ipaddress.ip_address(ip)
    except ValueError:
        raise IPNotAllowed() from None
    for entry in settings.login_allowed_ips:
        try:
            if "/" in entry:
                if addr in ipaddress.ip_network(entry, strict=False):
                    return
            elif addr == ipaddress.ip_address(entry):
                return
        except ValueError:
            continue  # entrada mal escrita en la config — se ignora, no se cae
    raise IPNotAllowed()


def check_rate_limit(ip: str) -> None:
    """Levanta RateLimitExceeded si esta IP (o el agregado global) ya
    debería estar bloqueada — no registra nada por sí sola, eso lo hace
    record_failed_attempt después de confirmar que el token era inválido."""
    global _global_locked_until

    settings = get_settings()
    now = time.time()
    window = settings.login_rate_limit_window_seconds

    ip_locked_until = _locked_until_by_ip.get(ip, 0.0)
    if now < ip_locked_until:
        raise RateLimitExceeded(int(ip_locked_until - now) + 1)
    if now < _global_locked_until:
        raise RateLimitExceeded(int(_global_locked_until - now) + 1)

    _prune(_failed_by_ip[ip], now - window)
    _prune(_global_failed, now - window)

    if len(_failed_by_ip[ip]) >= settings.login_rate_limit_attempts:
        _strikes_by_ip[ip] += 1
        lockout = _lockout_seconds(_strikes_by_ip[ip], window)
        _locked_until_by_ip[ip] = now + lockout
        raise RateLimitExceeded(lockout)

    if len(_global_failed) >= settings.login_rate_limit_global_attempts:
        _global_locked_until = now + settings.login_rate_limit_global_lockout_seconds
        raise RateLimitExceeded(settings.login_rate_limit_global_lockout_seconds)


def record_failed_attempt(ip: str) -> None:
    now = time.time()
    _failed_by_ip[ip].append(now)
    _global_failed.append(now)


def record_successful_attempt(ip: str) -> None:
    """Un login correcto limpia el historial de esa IP — no tiene sentido
    seguir contando en contra de alguien que ya demostró tener el token
    real. El contador global NO se toca: sigue reflejando cuánto ruido de
    intentos fallidos hubo en la ventana, venga de quien venga."""
    _failed_by_ip.pop(ip, None)
    _strikes_by_ip.pop(ip, None)
    _locked_until_by_ip.pop(ip, None)


def _hash(token: str) -> str:
    return hashlib.sha256(token.encode()).hexdigest()


def auth_status() -> dict:
    """Para GET /api/auth/status: si ya hay un token configurado, sin
    exponer nada del secreto en sí."""
    if not AUTH_FILE.exists():
        return {"configured": False}
    try:
        data = yaml.safe_load(AUTH_FILE.read_text()) or {}
    except yaml.YAMLError:
        return {"configured": False}
    return {"configured": bool(data.get("token_hash")), "created_at": data.get("created_at")}


def verify_token(raw_token: str) -> None:
    """Levanta NoTokenConfigured o InvalidToken; no devuelve nada si el
    token es válido."""
    if not AUTH_FILE.exists():
        raise NoTokenConfigured()
    try:
        data = yaml.safe_load(AUTH_FILE.read_text()) or {}
    except yaml.YAMLError as exc:
        raise NoTokenConfigured() from exc
    expected_hash = data.get("token_hash")
    if not expected_hash or not secrets.compare_digest(_hash(raw_token), expected_hash):
        raise InvalidToken()


def start_session() -> str:
    session_token = secrets.token_urlsafe(32)
    _sessions[session_token] = {"expires_at": time.time() + SESSION_TTL_SECONDS}
    return session_token


def client_ip(request) -> str:
    """La IP a usar para el rate limit — ver Settings.trust_proxy_headers
    sobre por qué X-Forwarded-For NUNCA se lee salvo que se active
    explícitamente esa opción."""
    settings = get_settings()
    if settings.trust_proxy_headers:
        forwarded = request.headers.get("x-forwarded-for")
        if forwarded:
            return forwarded.split(",")[0].strip()
    return request.client.host if request.client else "unknown"


def get_local_session(session: str | None = Cookie(default=None, alias=SESSION_COOKIE)) -> dict:
    if not session:
        raise HTTPException(status.HTTP_401_UNAUTHORIZED, "No hay sesión local — ingresá el token primero.")
    data = _sessions.get(session)
    if not data or data["expires_at"] < time.time():
        _sessions.pop(session, None)
        raise HTTPException(status.HTTP_401_UNAUTHORIZED, "Sesión local vencida — ingresá el token de nuevo.")
    return data


def end_session(session: str | None) -> None:
    if session:
        _sessions.pop(session, None)
