"""Puente hacia el subsistema de plugins, que vive en Go
(asterion-core/internal/plugins) — mismo criterio que runtime_bridge.py:
backend-core nunca reimplementa git clone, el arranque de procesos, ni el
cifrado de la configuración de un plugin. Le pide al mismo binario
`asterion` que ya usa el usuario, con --json, y parsea el mismo JSON que
esos comandos ya imprimen — un solo lugar con esa lógica, sea que se mire
desde la terminal o desde este dashboard.
"""

import json
import subprocess

from app.asterion_bin import find_asterion_binary


class PluginBridgeError(Exception):
    pass


def _run(args: list[str], timeout: int = 15) -> dict | list:
    binary = find_asterion_binary()
    if not binary:
        raise PluginBridgeError(
            "No encontré el binario 'asterion' en PATH ni en la variable ASTERION_BIN. "
            "Compilalo con 'go build -o asterion ./cmd/asterion' en asterion-core/ y agregalo "
            "al PATH, o fijá ASTERION_BIN a su ruta completa."
        )
    try:
        result = subprocess.run(
            [binary, "plugin", *args], capture_output=True, text=True, timeout=timeout
        )
    except subprocess.TimeoutExpired as exc:
        raise PluginBridgeError(f"'asterion plugin {' '.join(args)}' no respondió a tiempo") from exc

    try:
        parsed = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        message = result.stderr.strip() or result.stdout.strip() or f"'asterion plugin {' '.join(args)}' falló"
        raise PluginBridgeError(message) from exc

    if result.returncode != 0:
        raise PluginBridgeError(result.stderr.strip() or "el comando terminó con error")
    return parsed


def list_plugins() -> list[dict]:
    return _run(["list"])


def status(name: str) -> dict:
    return _run(["status", name])


def install(repo_url: str, name: str | None = None) -> dict:
    """git clone puede tardar más que el resto de las llamadas — timeout
    más generoso que el default."""
    args = ["install", repo_url, "--json"]
    if name:
        args += ["--name", name]
    return _run(args, timeout=60)


def start(name: str) -> dict:
    return _run(["start", name, "--json"], timeout=20)


def stop(name: str) -> dict:
    return _run(["stop", name, "--json"])


def remove(name: str) -> dict:
    return _run(["remove", name, "--json"])


def set_config(name: str, values: dict[str, str]) -> dict:
    if not values:
        raise PluginBridgeError("no se pasó ningún campo de configuración")
    pairs = [f"{key}={value}" for key, value in values.items()]
    return _run(["config", "set", name, *pairs, "--json"])


def show_config(name: str) -> dict:
    return _run(["config", "show", name])


def connect(name: str, project_id: int) -> dict:
    return _run(["connect", name, "--project", str(project_id), "--json"])
