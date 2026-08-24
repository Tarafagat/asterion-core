"""Encuentra el binario `asterion` en PATH o en ASTERION_BIN. Lo comparten
runtime_bridge.py y plugin_bridge.py — los dos puentes hacia comandos del
CLI (Go) que backend-core corre como subproceso en vez de reimplementar.
"""

import os
import shutil


def find_asterion_binary() -> str | None:
    env_path = os.getenv("ASTERION_BIN")
    if env_path and os.path.isfile(env_path):
        return env_path
    return shutil.which("asterion")
