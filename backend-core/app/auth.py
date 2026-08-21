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
import secrets
import time

import yaml
from fastapi import Cookie, HTTPException, status

from app.config import CLI_CONFIG_DIR

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
