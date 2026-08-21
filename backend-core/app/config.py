import os
from functools import lru_cache
from pathlib import Path

from dotenv import load_dotenv

APP_DIR = Path(__file__).resolve().parent.parent
CLI_CONFIG_DIR = Path.home() / ".config" / "asterion"

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
