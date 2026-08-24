"""API de plugins para frontend-core. Todo lo que hace de verdad (git
clone, arrancar/parar el proceso, cifrar la config) vive en Go — ver
app/plugin_bridge.py. Este router es una traducción delgada a HTTP más un
reverse proxy hacia la API que expone cada plugin en su propio puerto local
(proxy_to_plugin), para que el dashboard pueda hablarle a un plugin sin que
el navegador necesite saber en qué puerto quedó corriendo."""

import requests
from fastapi import APIRouter, Depends, HTTPException, Request, Response, status
from pydantic import BaseModel
from starlette.concurrency import run_in_threadpool

from app import plugin_bridge
from app.auth import get_local_session

router = APIRouter(prefix="/api/plugins", tags=["plugins"])


class PluginInstallRequest(BaseModel):
    repo_url: str
    name: str | None = None


class PluginConfigRequest(BaseModel):
    values: dict[str, str]


class PluginConnectRequest(BaseModel):
    project_id: int


def _handle(call):
    try:
        return call()
    except plugin_bridge.PluginBridgeError as exc:
        raise HTTPException(status.HTTP_502_BAD_GATEWAY, str(exc)) from exc


@router.get("")
def list_plugins(_: dict = Depends(get_local_session)) -> list[dict]:
    return _handle(plugin_bridge.list_plugins)


@router.post("/install", status_code=status.HTTP_201_CREATED)
def install_plugin(payload: PluginInstallRequest, _: dict = Depends(get_local_session)) -> dict:
    return _handle(lambda: plugin_bridge.install(payload.repo_url, payload.name))


@router.get("/{name}")
def read_plugin_status(name: str, _: dict = Depends(get_local_session)) -> dict:
    return _handle(lambda: plugin_bridge.status(name))


@router.delete("/{name}")
def remove_plugin(name: str, _: dict = Depends(get_local_session)) -> dict:
    return _handle(lambda: plugin_bridge.remove(name))


@router.post("/{name}/start")
def start_plugin(name: str, _: dict = Depends(get_local_session)) -> dict:
    return _handle(lambda: plugin_bridge.start(name))


@router.post("/{name}/stop")
def stop_plugin(name: str, _: dict = Depends(get_local_session)) -> dict:
    return _handle(lambda: plugin_bridge.stop(name))


@router.get("/{name}/config")
def read_plugin_config(name: str, _: dict = Depends(get_local_session)) -> dict:
    """Config ya enmascarada del lado de Go (plugins.GetConfigMasked) — los
    campos 'secret' del manifiesto nunca llegan acá en texto plano."""
    return _handle(lambda: plugin_bridge.show_config(name))


@router.put("/{name}/config")
def update_plugin_config(name: str, payload: PluginConfigRequest, _: dict = Depends(get_local_session)) -> dict:
    return _handle(lambda: plugin_bridge.set_config(name, payload.values))


@router.post("/{name}/connect")
def connect_plugin(name: str, payload: PluginConnectRequest, _: dict = Depends(get_local_session)) -> dict:
    """Vincula el plugin a un proyecto de Asterion Cloud — requiere que
    esta máquina ya tenga una sesión de 'asterion cloud login' guardada
    (el CLI la resuelve solo, backend-core no maneja esa sesión)."""
    return _handle(lambda: plugin_bridge.connect(name, payload.project_id))


@router.api_route("/{name}/proxy/{path:path}", methods=["GET", "POST", "PUT", "PATCH", "DELETE"])
async def proxy_to_plugin(name: str, path: str, request: Request, _: dict = Depends(get_local_session)) -> Response:
    """Reenvía a http://127.0.0.1:<puerto del plugin>/<path> — así el
    navegador solo le habla a backend-core, nunca directo a un puerto de
    plugin que puede cambiar entre reinicios. La cookie de sesión de
    backend-core se filtra a propósito (no tiene sentido para el plugin, y
    no hay razón para que un proceso de un tercero la reciba)."""
    installed = _handle(lambda: plugin_bridge.status(name))
    if installed.get("status") != "running":
        raise HTTPException(
            status.HTTP_503_SERVICE_UNAVAILABLE,
            f"El plugin {name!r} no está corriendo — arrancalo con 'asterion plugin start {name}' primero.",
        )
    port = installed["port"]
    body = await request.body()
    forward_headers = {
        k: v for k, v in request.headers.items() if k.lower() not in ("host", "content-length", "cookie")
    }

    def _forward() -> requests.Response:
        return requests.request(
            request.method,
            f"http://127.0.0.1:{port}/{path}",
            params=request.query_params,
            data=body or None,
            headers=forward_headers,
            timeout=30,
        )

    try:
        upstream = await run_in_threadpool(_forward)
    except requests.RequestException as exc:
        raise HTTPException(status.HTTP_502_BAD_GATEWAY, f"No pude conectar con el plugin {name!r}: {exc}") from exc
    return Response(
        content=upstream.content,
        status_code=upstream.status_code,
        media_type=upstream.headers.get("content-type"),
    )
