# frontend-core

El dashboard local de Asterion Core: login con un token propio (sin
Google, sin Firebase), esta máquina (SO, CPU, RAM, disco, física/VM/
contenedor), uso actual, costo mensual estimado, y el estado del Runtime
Engine (`asterion local status`/`doctor`). React + Vite + TS, sin
Tailwind ni router — es intencionalmente chico, una sola pantalla. Habla
exclusivamente con `../backend-core` (nunca con la API de Asterion Cloud
directo).

## Requisitos

Solo **Node + pnpm**. Nada más — no depende de Firebase, de una cuenta de
Google, ni de ninguna variable de entorno para funcionar (no hay
`.env` que configurar).

## Instalación

```bash
pnpm install
```

## Build (lo que sirve backend-core)

```bash
pnpm build   # genera dist/, que backend-core sirve como estático
```

`asterion local serve` espera este `dist/` ya generado — no lo compila
solo. Volvé a correr `pnpm build` después de cualquier cambio.

## Desarrollo (con hot reload)

```bash
BACKEND_CORE_PORT=8091 venv/bin/python -m app.main   # en ../backend-core, en otra terminal
pnpm dev                                              # acá, sirve en :5173 y proxyea /api al puerto de arriba
```

Para loguearte en modo desarrollo necesitás un token real: corré
`asterion local auth rotate` desde la terminal (o `asterion local serve`
si es la primera vez) y pegalo en la pantalla de login.
