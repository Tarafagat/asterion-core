"""API del túnel público (Cloudflare Tunnel) para frontend-core. Todo lo
que hace de verdad (arrancar/parar cloudflared, resolver qué puerto
exponer) vive en Go — ver app/tunnel_bridge.py. Este router es una
traducción delgada a HTTP, mismo criterio que routers/plugins.py."""

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel

from app import tunnel_bridge
from app.auth import get_local_session

router = APIRouter(prefix="/api/tunnel", tags=["tunnel"])


class TunnelStartRequest(BaseModel):
    # Sin esto, el CLI resuelve solo: el plugin principal si hay uno
    # corriendo, o si no el puerto de 'local serve' — ver
    # cmd/asterion/local_tunnel.go::resolveTunnelPort.
    plugin: str | None = None


def _handle(call):
    try:
        return call()
    except tunnel_bridge.TunnelBridgeError as exc:
        raise HTTPException(status.HTTP_502_BAD_GATEWAY, str(exc)) from exc


@router.get("")
def tunnel_status(_: dict = Depends(get_local_session)) -> dict:
    return _handle(tunnel_bridge.status)


@router.post("/start")
def start_tunnel(payload: TunnelStartRequest = TunnelStartRequest(), _: dict = Depends(get_local_session)) -> dict:
    return _handle(lambda: tunnel_bridge.start(payload.plugin))


@router.post("/stop")
def stop_tunnel(_: dict = Depends(get_local_session)) -> dict:
    return _handle(tunnel_bridge.stop)
