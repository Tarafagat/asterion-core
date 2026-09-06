"""Puente hacia 'asterion local tunnel', que vive en Go
(asterion-core/internal/tunnel) — mismo criterio que plugin_bridge.py y
runtime_bridge.py: backend-core nunca reimplementa el manejo de
cloudflared, le pide al mismo binario `asterion` que ya usa el usuario,
con --json.

Nota de prefijo: a diferencia de plugin_bridge.py (que antepone "plugin"
a cada comando), acá el comando real es `asterion local tunnel <verbo>`
— dos niveles, no uno.
"""

import json
import subprocess

from app.asterion_bin import find_asterion_binary


class TunnelBridgeError(Exception):
    pass


def _run(args: list[str], timeout: int = 20) -> dict:
    binary = find_asterion_binary()
    if not binary:
        raise TunnelBridgeError(
            "No encontré el binario 'asterion' en PATH ni en la variable ASTERION_BIN. "
            "Compilalo con 'go build -o asterion ./cmd/asterion' en asterion-core/ y agregalo "
            "al PATH, o fijá ASTERION_BIN a su ruta completa."
        )
    try:
        result = subprocess.run(
            [binary, "local", "tunnel", *args], capture_output=True, text=True, timeout=timeout
        )
    except subprocess.TimeoutExpired as exc:
        raise TunnelBridgeError(f"'asterion local tunnel {' '.join(args)}' no respondió a tiempo") from exc

    try:
        parsed = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        message = result.stderr.strip() or result.stdout.strip() or f"'asterion local tunnel {' '.join(args)}' falló"
        raise TunnelBridgeError(message) from exc

    if result.returncode != 0:
        raise TunnelBridgeError(result.stderr.strip() or "el comando terminó con error")
    return parsed


def status() -> dict:
    return _run(["status", "--json"])


def start(plugin: str | None = None) -> dict:
    # Timeout más generoso: 'start' espera hasta 15s a que cloudflared
    # imprima la URL del quick tunnel antes de devolver el control.
    args = ["start", "--json"]
    if plugin:
        args += ["--plugin", plugin]
    return _run(args, timeout=25)


def stop() -> dict:
    return _run(["stop", "--json"])
