# backend-core

Servicio local (FastAPI/Python) detrás de `asterion local serve`: sirve el
dashboard de `frontend-core` y su mini API — login con un token propio,
métricas reales de esta máquina (`psutil`) y un costo mensual estimado.
Corre 100% en la máquina del usuario, en un puerto separado de Asterion
Cloud, y no depende de MySQL, Redis, ni de ningún servicio externo para
funcionar.

Open source: solo visualiza y estima costo, nunca factura — ver la
sección "Dashboard local" en [../README.md](../README.md).

## Requisitos

Nada externo. Sin Firebase, sin cuenta de Google, sin Asterion Cloud, sin
base de datos. Solo:

1. **Python 3.10+** para este servicio.
2. **Node + pnpm** para compilar `frontend-core` una vez (ver su propio
   README) — el `dist/` resultante lo sirve este mismo servicio.

## Cómo funciona el login (sin Google/Firebase)

El acceso lo controla un token que genera el CLI (`asterion local serve`,
Go — ver `asterion-core/internal/localauth`), **no** este servicio: la
primera vez que corrés `asterion local serve`, si no existe
`~/.config/asterion/local-auth.yaml`, el CLI genera un token aleatorio, lo
imprime UNA sola vez en la terminal, y guarda solo su hash SHA-256 en ese
YAML (permisos `0600` — solo tu usuario del sistema operativo puede
leerlo). backend-core nunca genera ni ve el token en texto plano: cuando
el navegador manda uno (`POST /api/auth/token`), lo hashea y lo compara
contra ese mismo archivo.

Si perdiste el token, no hay forma de recuperarlo (solo se guardó el
hash) — `asterion local auth rotate` genera uno nuevo e invalida el
anterior.

## Instalación

```bash
python3 -m venv venv
venv/bin/pip install -r requirements.txt
```

Config por entorno, mismo esquema que `backend/` (Asterion Cloud): se
carga `.env` y encima `.env.<APP_ENV>` (`APP_ENV` default `development`),
pisando lo que se solape. `.env.example` es la plantilla si armás un
entorno nuevo — pero ningún valor ahí es obligatorio para arrancar.

```bash
venv/bin/python -m app.main                      # development (default)
APP_ENV=production venv/bin/python -m app.main    # production
```

## Levantar

Normalmente no se corre directo — `asterion local serve` (el CLI) lo hace
por vos, buscándolo en `../backend-core` relativo a donde lo corras (y
generando/reusando el token de acceso antes de arrancarlo). Para correrlo
manualmente (debug, o si `asterion local serve` no lo encuentra):

```bash
venv/bin/python -m app.main
# [asterion backend-core] http://127.0.0.1:8091
```

`BACKEND_CORE_PORT=0` (default) hace que el sistema operativo elija un
puerto libre; fijalo en `.env` si preferís uno estable. Necesitás que
`~/.config/asterion/local-auth.yaml` ya exista (corré
`asterion local auth rotate` una vez si estás levantando esto sin pasar
por `asterion local serve`).

## Variables de entorno

| Variable | Obligatoria | Default | Qué hace |
|---|---|---|---|
| `APP_ENV` | No | `development` | Elige qué `.env.<APP_ENV>` cargar encima de `.env` (`development` o `production`). |
| `CLOUD_API_BASE_URL` | No | `http://localhost:8000` | De dónde refresca la tabla de precios en vivo si hay sesión de `asterion cloud login`. |
| `PRICING_REFRESH_MINUTES` | No | `60` | Cada cuánto se considera vieja la tabla de precios cacheada. |
| `BACKEND_CORE_PORT` | No | `0` (auto) | Puerto donde escucha. |
| `BACKEND_CORE_HOST` | No | `127.0.0.1` | Host donde escucha. |
| `CORS_ORIGINS` | No | `http://localhost:5173` | Para correr `frontend-core` con `pnpm dev` en paralelo (ver su README). |
| `FRONTEND_DIST_PATH` | No | `../frontend-core/dist` | Dónde buscar el build de frontend-core para servirlo. |
| `ASTERION_BIN` | No | (busca `asterion` en PATH) | Ruta al binario del CLI — lo necesitan `/api/runtime/*` (ver abajo). |

## Endpoints

Todos bajo `/api`, y todos menos `auth/*` requieren la cookie de sesión
local (`asterion_local_session`, HttpOnly) que deja `POST /api/auth/token`
tras un login válido.

- `GET /api/auth/status` — si ya hay un token configurado en esta máquina
  y desde cuándo (`created_at`) — nunca expone el secreto en sí.
- `POST /api/auth/token` — recibe `{token}`, lo hashea y lo compara contra
  `~/.config/asterion/local-auth.yaml`. `400` si todavía no se generó
  ningún token (correr `asterion local serve` primero), `401` si no
  coincide.
- `POST /api/auth/logout`
- `GET /api/me` — `{"authenticated": true}` si la sesión es válida.
- `GET /api/info` — identidad de la máquina (SO, CPU, RAM/disco totales, física/VM/contenedor).
- `GET /api/metrics` — uso actual (CPU%, RAM, disco, red) — datos crudos, sin costo.
- `POST /api/cost-estimate` — `{cpu_cores, ram_gb, storage_gb, storage_type}` → costo mensual estimado.
- `GET /api/pricing` — la tabla de precios vigente y su origen (`bundled-default` o `asterion-cloud-live`).
- `GET /api/runtime/status` — qué detectó el Runtime Engine en esta máquina
  (systemd, firewall, reverse proxy, tunnel) y la config vigente. No lo
  calcula acá: corre `asterion local status` como subproceso (ver
  `app/runtime_bridge.py`) y devuelve el mismo JSON — `503` si no
  encuentra el binario `asterion` (ver `ASTERION_BIN` arriba).
- `GET /api/runtime/doctor` — ídem con `asterion local doctor`: el
  checklist de salud del Runtime, SSH y análisis de riesgo de firewall.

## Troubleshooting

- **"Todavía no se generó un token" al intentar entrar**: corré
  `asterion local serve` (o `asterion local auth rotate`) desde la
  terminal al menos una vez — el token lo genera el CLI, no el navegador.
- **Token incorrecto (401) aunque estés seguro de que lo copiaste bien**:
  los tokens no se pueden mostrar dos veces (solo se guarda el hash) — si
  no tenés el que se imprimió la primera vez, `asterion local auth rotate`
  genera uno nuevo.
- **La sección "Runtime" del dashboard muestra un error 503**: no encontré
  el binario `asterion` — compilalo (`go build -o asterion ./cmd/asterion`
  en `asterion-core/`) y agregalo al `PATH`, o fijá `ASTERION_BIN` a su
  ruta completa en `.env`.
