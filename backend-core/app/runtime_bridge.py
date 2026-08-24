"""Puente hacia el Runtime Engine, que vive en Go (asterion-core/internal/runtime)
— no en Python. backend-core nunca reimplementa la detección de systemd/
firewall/reverse proxy/tunnel: le pide el mismo binario `asterion` que ya
usa el usuario, exactamente los mismos comandos que correría a mano
(`asterion local status`, `asterion local doctor`), y parsea el JSON que
esos comandos ya imprimen. Un solo lugar con esa lógica, sea que se mire
desde la terminal o desde este dashboard.
"""

import json
import subprocess

from app.asterion_bin import find_asterion_binary


class RuntimeBridgeError(Exception):
    pass


def _run(args: list[str]) -> dict:
    binary = find_asterion_binary()
    if not binary:
        raise RuntimeBridgeError(
            "No encontré el binario 'asterion' en PATH ni en la variable ASTERION_BIN. "
            "Compilalo con 'go build -o asterion ./cmd/asterion' en asterion-core/ y agregalo "
            "al PATH, o fijá ASTERION_BIN a su ruta completa."
        )
    try:
        result = subprocess.run(
            [binary, "local", *args], capture_output=True, text=True, timeout=10
        )
    except subprocess.TimeoutExpired as exc:
        raise RuntimeBridgeError(f"'asterion local {' '.join(args)}' no respondió a tiempo") from exc

    # 'doctor' devuelve exit code 1 cuando el chequeo encuentra problemas —
    # eso NO es un error de ejecución, el JSON en stdout sigue siendo
    # válido y es justamente lo que queremos mostrar. Un exit code
    # distinto de 0/1, o un stdout que no parsea, sí es un error real.
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise RuntimeBridgeError(
            f"'asterion local {' '.join(args)}' no devolvió JSON válido: {result.stderr.strip() or result.stdout.strip()}"
        ) from exc


def status() -> dict:
    return _run(["status"])


def doctor() -> dict:
    return _run(["doctor"])
