"""Precios para el cálculo de costo offline de Core: una tabla PÚBLICA
embebida (app/data/default_pricing.json) que sirve de base, y que se
refresca en vivo contra `GET /pricing` de Asterion Cloud cuando hay sesión
(`asterion cloud login`) y Cloud está alcanzable — quedando cacheada en
disco para seguir funcionando offline entre refrescos. El cálculo en sí
(`calculate_cost`) es el mismo que usa Cloud — ver shared/asterion_shared.
"""

import json
import time
from pathlib import Path

import requests
from asterion_shared import calculate_cost

from app.config import CLI_CONFIG_DIR, get_settings

DEFAULT_PRICING_PATH = Path(__file__).resolve().parent / "data" / "default_pricing.json"
# Cache en disco separado del bundle de solo-lectura del paquete, para no
# pisar el default que se reinstala con cada actualización de Core.
CACHE_PATH = CLI_CONFIG_DIR / "pricing_cache.json"


def _read_json(path: Path) -> dict | None:
    try:
        return json.loads(path.read_text())
    except (FileNotFoundError, ValueError, OSError):
        return None


def _load_cached_or_default() -> dict:
    return _read_json(CACHE_PATH) or _read_json(DEFAULT_PRICING_PATH) or {
        "updated_at": None,
        "source": "bundled-default",
        "prices": {},
    }


def _stale(table: dict) -> bool:
    updated_at = table.get("updated_at")
    if not updated_at:
        return True
    settings = get_settings()
    return (time.time() - updated_at) > settings.pricing_refresh_minutes * 60


def _cli_access_token() -> str | None:
    data = _read_json(CLI_CONFIG_DIR / "credentials.json")
    return data.get("access_token") if data else None


def _refresh_from_cloud() -> dict | None:
    token = _cli_access_token()
    if not token:
        return None
    settings = get_settings()
    try:
        response = requests.get(
            f"{settings.cloud_api_base_url}/pricing",
            headers={"Authorization": f"Bearer {token}"},
            timeout=3,
        )
        response.raise_for_status()
    except requests.RequestException:
        return None

    rows = response.json()
    prices = {row["resource_type"]: float(row["unit_price"]) for row in rows}
    table = {"updated_at": time.time(), "source": "asterion-cloud-live", "prices": prices}
    try:
        CLI_CONFIG_DIR.mkdir(parents=True, exist_ok=True)
        CACHE_PATH.write_text(json.dumps(table, indent=2))
    except OSError:
        pass
    return table


def current_prices() -> dict:
    """Devuelve {source, updated_at, prices}. Intenta refrescar en vivo si la
    tabla cacheada está vieja; si Cloud no está disponible (sin sesión, sin
    red), sigue funcionando con lo último que tenga guardado o el default
    embebido — nunca bloquea ni rompe la estimación offline."""
    table = _load_cached_or_default()
    if _stale(table):
        fresh = _refresh_from_cloud()
        if fresh:
            table = fresh
    return table


def estimate_cost(spec: dict) -> dict:
    table = current_prices()
    cost = calculate_cost(spec, table.get("prices", {}))
    return {**cost, "price_source": table.get("source"), "prices_updated_at": table.get("updated_at")}
