"""backend-core: el servicio local de Asterion Core (open source).

Sirve dos cosas en un único puerto, separado del backend de Asterion Cloud
(otro proceso, otro puerto, otro repo de credenciales):

  - GET  /api/*     la mini API (login con Google, métricas de esta
                     máquina, estimación de costo) — ver app/routers/api.py.
  - GET  /*          el build de frontend-core (dist/), el dashboard.

Solo visualización y cálculo de costo estimado: Core no factura a nadie —
eso es exclusivamente Asterion Cloud (backend/). Ver README.md.
"""

from pathlib import Path

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.staticfiles import StaticFiles

from app.config import get_settings
from app.routers.api import router as api_router

app = FastAPI(title="Asterion Core — backend-core", version="0.1.0")

settings = get_settings()
app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.cors_origins,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

app.include_router(api_router)

_dist = Path(settings.frontend_dist_path)
if _dist.is_dir():
    app.mount("/", StaticFiles(directory=str(_dist), html=True), name="frontend-core")
else:
    @app.get("/")
    def _no_frontend_built() -> dict:
        return {
            "detail": (
                "frontend-core todavía no tiene un build. Corré `pnpm build` en "
                "asterion-core/frontend-core/ y reiniciá backend-core."
            )
        }


def find_free_port(preferred: int = 8091, tries: int = 20) -> int:
    import socket

    for port in range(preferred, preferred + tries):
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            try:
                s.bind(("127.0.0.1", port))
                return port
            except OSError:
                continue
    # Sin ninguno libre en el rango: dejar que el SO elija uno.
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


if __name__ == "__main__":
    import uvicorn

    port = settings.port or find_free_port()
    print(f"[asterion backend-core] http://{settings.host}:{port}")
    uvicorn.run(app, host=settings.host, port=port)
