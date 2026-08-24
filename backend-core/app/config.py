import os
from functools import lru_cache
from pathlib import Path

from dotenv import load_dotenv

APP_DIR = Path(__file__).resolve().parent.parent


def _user_config_dir() -> Path:
    """Replica exactamente la resolución de os.UserConfigDir() de Go, que es
    lo que usa internal/cliconfig e internal/localauth (el CLI) para decidir
    dónde vive ~/.config/asterion/*.yaml|json — asumir "~/.config" a secas
    acá rompía login y el cache de precios en cualquier máquina donde Go NO
    resuelve a esa ruta, que es todo macOS (usa ~/Library/Application
    Support) y Windows (usa %AppData%). Si esta función alguna vez se separa
    de Go, los dos lados dejan de poder leerse los archivos del otro.
    """
    if os.name == "nt":
        appdata = os.getenv("AppData")
        return Path(appdata) if appdata else Path.home() / "AppData" / "Roaming"
    if os.uname().sysname == "Darwin":
        return Path.home() / "Library" / "Application Support"
    xdg = os.getenv("XDG_CONFIG_HOME")
    return Path(xdg) if xdg else Path.home() / ".config"


CLI_CONFIG_DIR = _user_config_dir() / "asterion"

# Mismo esquema que backend/app/core/config.py: APP_ENV elige qué archivo
# cargar encima del .env base (default "development"). Para producción:
# APP_ENV=production venv/bin/python -m app.main
APP_ENV = os.getenv("APP_ENV", "development")
load_dotenv(APP_DIR / ".env")
load_dotenv(APP_DIR / f".env.{APP_ENV}", override=True)


class Settings:
    app_env: str = APP_ENV

    # Puerto donde escucha este servicio. "0" = el sistema operativo elige
    # un puerto libre (ver app/main.py); ver `asterion local serve` en el
    # CLI, que hace exactamente eso por defecto.
    port: int = int(os.getenv("BACKEND_CORE_PORT", "0"))
    host: str = os.getenv("BACKEND_CORE_HOST", "127.0.0.1")

    # Asterion Cloud, para refrescar la tabla de precios en vivo cuando hay
    # sesión (ver app/pricing.py). Nunca se usa para nada más — Core no
    # depende de Cloud para funcionar, solo se enriquece si está disponible.
    cloud_api_base_url: str = os.getenv("CLOUD_API_BASE_URL", "http://localhost:8000")
    pricing_refresh_minutes: int = int(os.getenv("PRICING_REFRESH_MINUTES", "60"))

    cors_origins: list[str] = os.getenv("CORS_ORIGINS", "http://localhost:5173").split(",")

    # Rate limit de POST /api/auth/token — ver app/auth.py. Default: 5
    # intentos fallidos cada 5 minutos por IP, con bloqueo creciente si la
    # misma IP sigue insistiendo después de que se le vence cada bloqueo.
    login_rate_limit_attempts: int = int(os.getenv("LOGIN_RATE_LIMIT_ATTEMPTS", "5"))
    login_rate_limit_window_seconds: int = int(os.getenv("LOGIN_RATE_LIMIT_WINDOW_SECONDS", "300"))

    # Defensa contra alguien que va cambiando de IP para esquivar el límite
    # de arriba: además del conteo por IP, se cuenta el total de intentos
    # fallidos de TODAS las IPs juntas en la misma ventana — si se pasa de
    # este número (más alto, pensado para agregar muchas IPs con pocos
    # intentos cada una), se bloquea el login entero por un rato, sin
    # importar de qué IP venga el próximo intento.
    login_rate_limit_global_attempts: int = int(os.getenv("LOGIN_RATE_LIMIT_GLOBAL_ATTEMPTS", "20"))
    login_rate_limit_global_lockout_seconds: int = int(
        os.getenv("LOGIN_RATE_LIMIT_GLOBAL_LOCKOUT_SECONDS", "900")
    )

    # Lista blanca opcional de IPs/rangos (CIDR) que pueden intentar
    # loguearse — vacía (default) significa sin restricción, que es lo
    # correcto mientras el dashboard solo escucha en 127.0.0.1. Tiene
    # sentido activarla si se expone el dashboard más allá de localhost
    # (reverse proxy, tunnel — ver `asterion local doctor`).
    # Ejemplo: LOGIN_ALLOWED_IPS=192.168.1.0/24,203.0.113.7
    login_allowed_ips: list[str] = [
        entry.strip() for entry in os.getenv("LOGIN_ALLOWED_IPS", "").split(",") if entry.strip()
    ]

    # La IP del cliente para el rate limit sale de request.client.host por
    # default (la conexión TCP real, imposible de falsear a nivel HTTP). Si
    # ALGUNA VEZ hay un reverse proxy real y confiable delante (nginx,
    # Cloudflare Tunnel, etc.), activar esto para leer la IP original de
    # X-Forwarded-For — pero OJO: dejarlo prendido sin un proxy real de por
    # medio le permite a cualquiera falsear ese header y esquivar el rate
    # limit por completo, que es peor que no tenerlo. Default apagado.
    trust_proxy_headers: bool = os.getenv("TRUST_PROXY_HEADERS", "false").lower() in ("1", "true", "yes")

    # Dónde está el build de frontend-core (dist/) para servirlo como
    # archivos estáticos. `or` en vez de un segundo argumento a os.getenv:
    # .env.development/.env.production traen FRONTEND_DIST_PATH="" a
    # propósito (para poder pisarlo sin editar el archivo), y
    # os.getenv(K, default) NO cae al default cuando la variable está
    # seteada pero vacía — solo cuando no existe.
    frontend_dist_path: str = os.getenv("FRONTEND_DIST_PATH", "") or str(APP_DIR.parent / "frontend-core" / "dist")


@lru_cache
def get_settings() -> Settings:
    return Settings()
