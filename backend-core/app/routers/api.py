from fastapi import APIRouter, Depends, HTTPException, Request, Response, status
from pydantic import BaseModel

from app import auth, metrics, pricing, runtime_bridge
from app.auth import SESSION_COOKIE, end_session, get_local_session, start_session

router = APIRouter(prefix="/api")


class TokenLoginRequest(BaseModel):
    token: str


class CostEstimateRequest(BaseModel):
    cpu_cores: float
    ram_gb: float
    storage_gb: float
    storage_type: str = "ssd"


@router.get("/auth/status")
def read_auth_status() -> dict:
    return auth.auth_status()


@router.post("/auth/token", status_code=status.HTTP_200_OK)
def login_with_token(payload: TokenLoginRequest, response: Response) -> dict:
    try:
        auth.verify_token(payload.token)
    except auth.NoTokenConfigured as exc:
        raise HTTPException(
            status.HTTP_400_BAD_REQUEST,
            "Todavía no se generó un token de acceso en esta máquina — corré 'asterion local serve' "
            "(o 'asterion local auth rotate') al menos una vez primero.",
        ) from exc
    except auth.InvalidToken as exc:
        raise HTTPException(status.HTTP_401_UNAUTHORIZED, "Token incorrecto.") from exc

    session_token = start_session()
    response.set_cookie(SESSION_COOKIE, session_token, httponly=True, samesite="lax", max_age=12 * 3600)
    return {"ok": True}


@router.post("/auth/logout")
def logout(request: Request, response: Response) -> dict:
    end_session(request.cookies.get(SESSION_COOKIE))
    response.delete_cookie(SESSION_COOKIE)
    return {"ok": True}


@router.get("/me")
def me(_: dict = Depends(get_local_session)) -> dict:
    return {"authenticated": True}


@router.get("/info")
def info(_: dict = Depends(get_local_session)) -> dict:
    return metrics.gather_info()


@router.get("/metrics")
def snapshot(_: dict = Depends(get_local_session)) -> dict:
    return metrics.collect_snapshot()


@router.post("/cost-estimate")
def cost_estimate(payload: CostEstimateRequest, _: dict = Depends(get_local_session)) -> dict:
    return pricing.estimate_cost(payload.model_dump())


@router.get("/pricing")
def pricing_table(_: dict = Depends(get_local_session)) -> dict:
    return pricing.current_prices()


@router.get("/runtime/status")
def runtime_status(_: dict = Depends(get_local_session)) -> dict:
    """Delegado al Runtime Engine (Go): qué se detectó en esta máquina
    (systemd, firewall, reverse proxy, tunnel) y la configuración vigente.
    Ver app/runtime_bridge.py — nunca se reimplementa esa detección acá."""
    try:
        return runtime_bridge.status()
    except runtime_bridge.RuntimeBridgeError as exc:
        raise HTTPException(status.HTTP_503_SERVICE_UNAVAILABLE, str(exc)) from exc


@router.get("/runtime/doctor")
def runtime_doctor(_: dict = Depends(get_local_session)) -> dict:
    try:
        return runtime_bridge.doctor()
    except runtime_bridge.RuntimeBridgeError as exc:
        raise HTTPException(status.HTTP_503_SERVICE_UNAVAILABLE, str(exc)) from exc
