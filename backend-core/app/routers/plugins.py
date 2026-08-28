"""API de plugins para frontend-core. Todo lo que hace de verdad (git
clone, arrancar/parar el proceso, cifrar la config) vive en Go — ver
app/plugin_bridge.py. Este router es una traducción delgada a HTTP más un
reverse proxy hacia la API que expone cada plugin en su propio puerto local
(proxy_to_plugin), para que el dashboard pueda hablarle a un plugin sin que
el navegador necesite saber en qué puerto quedó corriendo."""

from pathlib import Path

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
    # True: repo_url es una carpeta ya existente en este disco (no una URL
    # de git) — ver 'asterion plugin install --link' / plugins.InstallLinked.
    link: bool = False


class PluginBrowseEntry(BaseModel):
    name: str
    path: str
    has_manifest: bool


class PluginBrowseResult(BaseModel):
    path: str
    parent: str | None
    has_manifest: bool
    entries: list[PluginBrowseEntry]


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


def _has_manifest(dir_path: Path) -> bool:
    try:
        return (dir_path / "plugin.yaml").is_file()
    except OSError:
        return False


# Registrado antes de GET /{name} a propósito: Starlette matchea rutas en
# orden de registro, y "/browse-dirs" calzaría con el patrón {name} si
# quedara después (llamando por error a read_plugin_status("browse-dirs")).
@router.get("/browse-dirs", response_model=PluginBrowseResult)
def browse_dirs(path: str | None = None, _: dict = Depends(get_local_session)) -> PluginBrowseResult:
    """Navegador de carpetas puro filesystem, de solo lectura — no pasa por
    el binario asterion (no hay comando CLI equivalente, es solo listar
    directorios). Existe para que el selector de carpeta del dashboard
    pueda ofrecer la ruta absoluta real en disco: la File API estándar del
    navegador nunca expone esa ruta fuera de Electron, así que "elegir
    carpeta" acá se resuelve navegando por HTTP contra este mismo backend,
    que sí tiene acceso directo al filesystem local."""
    target = Path(path).expanduser() if path else Path.home()
    try:
        target = target.resolve(strict=True)
    except (FileNotFoundError, RuntimeError) as exc:
        raise HTTPException(status.HTTP_404_NOT_FOUND, f"No existe la carpeta {target}") from exc
    if not target.is_dir():
        raise HTTPException(status.HTTP_400_BAD_REQUEST, f"{target} no es una carpeta")

    try:
        children = sorted(
            (p for p in target.iterdir() if not p.name.startswith(".") and p.is_dir()),
            key=lambda p: p.name.lower(),
        )
    except PermissionError as exc:
        raise HTTPException(status.HTTP_403_FORBIDDEN, f"Sin permiso para leer {target}") from exc

    entries = [
        PluginBrowseEntry(name=child.name, path=str(child), has_manifest=_has_manifest(child))
        for child in children
    ]
    parent = str(target.parent) if target.parent != target else None
    return PluginBrowseResult(
        path=str(target), parent=parent, has_manifest=_has_manifest(target), entries=entries
    )


@router.post("/install", status_code=status.HTTP_201_CREATED)
def install_plugin(payload: PluginInstallRequest, _: dict = Depends(get_local_session)) -> dict:
    return _handle(lambda: plugin_bridge.install(payload.repo_url, payload.name, payload.link))


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
