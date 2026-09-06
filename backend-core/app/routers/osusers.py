"""API de usuarios de sistema para frontend-core. Todo lo que hace de
verdad (useradd, sudo, claves SSH) vive en Go — ver app/osuser_bridge.py.
Este router es una traducción delgada a HTTP, mismo criterio que
routers/plugins.py."""

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field

from app import osuser_bridge
from app.auth import get_local_session

router = APIRouter(prefix="/api/os-users", tags=["os-users"])

LEVEL_PATTERN = "^(admin|operador|solo_lectura)$"
UNIX_NAME_PATTERN = r"^[a-z_][a-z0-9_-]{0,31}$"


class OsUserCreateRequest(BaseModel):
    username: str = Field(pattern=UNIX_NAME_PATTERN)
    level: str = Field(pattern=LEVEL_PATTERN)
    groups: list[str] = Field(default_factory=list)
    public_key: str | None = None
    generate_key: bool = False


def _handle(call):
    try:
        return call()
    except osuser_bridge.OsUserBridgeError as exc:
        raise HTTPException(status.HTTP_502_BAD_GATEWAY, str(exc)) from exc


@router.get("")
def list_os_users(_: dict = Depends(get_local_session)) -> list[dict]:
    return _handle(osuser_bridge.list_managed)


@router.post("", status_code=status.HTTP_201_CREATED)
def create_os_user(payload: OsUserCreateRequest, _: dict = Depends(get_local_session)) -> dict:
    return _handle(
        lambda: osuser_bridge.create(
            payload.username, payload.level, payload.groups, payload.public_key, payload.generate_key
        )
    )


@router.delete("/{username}")
def remove_os_user(username: str, _: dict = Depends(get_local_session)) -> dict:
    return _handle(lambda: osuser_bridge.remove(username))
