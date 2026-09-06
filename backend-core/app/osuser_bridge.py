"""Puente hacia 'asterion local user', que vive en Go
(asterion-core/internal/osuser) — mismo criterio que plugin_bridge.py:
backend-core nunca reimplementa useradd/sudo/SSH keys, le pide al mismo
binario `asterion` que ya usa el usuario, con --json, y parsea el mismo
JSON que ese comando ya imprime.
"""

import json
import subprocess

from app.asterion_bin import find_asterion_binary


class OsUserBridgeError(Exception):
    pass


def _run(args: list[str], timeout: int = 20) -> dict | list:
    binary = find_asterion_binary()
    if not binary:
        raise OsUserBridgeError(
            "No encontré el binario 'asterion' en PATH ni en la variable ASTERION_BIN. "
            "Compilalo con 'go build -o asterion ./cmd/asterion' en asterion-core/ y agregalo "
            "al PATH, o fijá ASTERION_BIN a su ruta completa."
        )
    try:
        result = subprocess.run(
            [binary, "local", "user", *args], capture_output=True, text=True, timeout=timeout
        )
    except subprocess.TimeoutExpired as exc:
        raise OsUserBridgeError(f"'asterion local user {' '.join(args)}' no respondió a tiempo") from exc

    try:
        parsed = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        message = result.stderr.strip() or result.stdout.strip() or f"'asterion local user {' '.join(args)}' falló"
        raise OsUserBridgeError(message) from exc

    if result.returncode != 0:
        raise OsUserBridgeError(result.stderr.strip() or "el comando terminó con error")
    return parsed


def list_managed() -> list[dict]:
    return _run(["list"])


def create(
    username: str,
    level: str,
    groups: list[str] | None = None,
    public_key: str | None = None,
    generate_key: bool = False,
) -> dict:
    args = ["create", username, "--level", level, "--json"]
    if groups:
        args += ["--group", ",".join(groups)]
    if generate_key:
        args += ["--generate-key"]
    elif public_key:
        args += ["--ssh-key", public_key]
    return _run(args)


def remove(username: str) -> dict:
    return _run(["remove", username, "--json"])
