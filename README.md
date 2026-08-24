# Asterion Core

**Open source, licencia Apache-2.0** (ver [LICENSE](LICENSE) y la sección
"Licencia" al final de este archivo). Asterion Core es todo lo que no
depende de facturarle a un cliente: el CLI, el Remote Agent, el sistema de
plugins, el motor de Provider Adapters (Go), y el dashboard local
(`backend-core` + `frontend-core`) con visualización de métricas y cálculo
de costo *estimado*. Self-hosted, sin límite artificial de servidores,
proyectos ni instancias. Asterion Cloud (repo `asterion-cloud`, licencia
AGPLv3) es el control plane administrado — el único lugar donde ese costo
estimado se convierte en una factura real, en fleet management, y en el
resto de los servicios pagos — Core nunca cobra ni factura a nadie, solo
estima y muestra.

Motor de aprovisionamiento multi-nube de Asterion, en Go. Un solo lugar
para la lógica de proveedores (AWS/Azure/GCP/OCI, y los que se agreguen
después), consumido tanto por el CLI como por la API de Asterion — nadie
más duplica esa integración:

```
        Asterion Core (Go)
         /              \
      CLI (Go)      API de Asterion (FastAPI/Python)
    cmd/asterion         vía app/services/core_client.py
         \              /
      Provider Adapters (internal/adapters)
     AWS · Azure · GCP · OCI · (el que sigue)
```

- El **CLI** habla con la **API de Asterion** para todo lo que es estado,
  permisos, sesión y auditoría (login, crear un proyecto, conectar una
  cuenta cloud, aplicar un plan). No le pega directo a un proveedor, y
  tampoco sabe nada de cómo la API arma o valida una sesión (Firebase u
  otra cosa) — eso es un detalle interno de la API. Así la API sigue siendo
  la que aplica RBAC y deja rastro en `audit_logs`.
- La **API de Asterion** le delega a **asterion-core** cualquier operación
  que implique hablarle de verdad a un proveedor — nunca reimplementa esa
  integración en Python.
- El **CLI** sí le habla directo a asterion-core para lecturas públicas que
  no dependen de un proyecto ni de sesión: `capabilities`, `providers`.

## Local-first, Cloud opcional

`asterion local` responde preguntas sobre la máquina donde corre el CLI
mismo — distinto de `instances` (un inventario de OTROS hosts) y de
`cloud` (vincularse con Asterion Cloud). Siempre devuelve **datos crudos,
nunca un costo**: cuánto sale ese consumo es un cálculo aparte que hace el
pricing engine del lado de Asterion Cloud, con la tarifa vigente del
proyecto, una vez que la instancia esté conectada. `asterion local` es la
capa de "analista de sistema" (qué es esta máquina, cuánto está usando);
la tarifa es una preocupación de otra capa, más adelante en la cadena.

```bash
asterion local info    # qué es: SO, arquitectura, CPU, RAM y disco totales, física/VM/contenedor
asterion local stats    # cuánto usa ahora: CPU%, RAM, disco, datos de red — sin costo
asterion local stats --watch --interval 5s   # repetir la medición
```

El CLI tiene dos modos sobre la misma idea de "instancia", pensados para
que un usuario pueda empezar completamente local y gratis, y recién
después decidir engancharse a Asterion Cloud sin recrear nada:

- **Local** (`asterion instances add/list/connect/remove`): un inventario
  de hosts SSH 100% en tu máquina (`~/.config/asterion/instances.json`).
  No requiere sesión ni toca la API de Asterion en absoluto.
- **Cloud** (`asterion cloud connect`, `asterion cloud install-agent`):
  vincula una instancia local a un proyecto de Asterion Cloud. La
  identidad real del recurso es su id local (`inst_xxxxxxxx`, generado una
  sola vez al crearla) — se manda como `external_ref` al conectar, y si
  ese `external_ref` ya estaba conectado, la API **reusa la misma fila**
  en vez de crear una nueva. Local y Cloud son dos modos de administración
  sobre el mismo recurso, nunca dos instancias distintas.

Una vez conectada, `asterion cloud install-agent` deja un servicio
(`systemd --user` en Linux) corriendo `asterion agent-run` en la máquina,
que reporta CPU/RAM/disco reales cada un minuto a la API — autenticado con
una API key propia de esa instancia, no con la sesión del usuario.

## Dashboard local: backend-core + frontend-core

`asterion local serve` es un tercer servicio, distinto del agente de arriba
y de asterion-core (el de los Provider Adapters): un dashboard web que
corre 100% en tu máquina para ver en el navegador lo mismo que
`asterion local info/stats` muestra en la terminal — más un costo estimado
calculado ahí mismo.

**El login ya no es con Google/Firebase.** Antes verificaba un ID token de
Google contra Firebase, lo que exigía tener un proyecto de Firebase real
solo para poder abrir un dashboard local. Ahora el acceso lo administra un
**token propio, generado por el CLI** (`internal/localauth`, Go) y guardado
en un YAML — cero dependencias externas:

```
   asterion local serve (CLI, Go)
          │
          ├─ ¿existe ~/.config/asterion/local-auth.yaml?
          │     no → genera un token, lo imprime UNA vez en la terminal,
          │          guarda solo su hash SHA-256 en el YAML (permisos 0600)
          │     sí → lo reusa (no puede volver a mostrarlo: solo tiene el hash)
          │
          ▼
   backend-core (FastAPI/Python)
          │
          ├─ POST /api/auth/token {token} → hashea, compara contra
          │  local-auth.yaml, si coincide arranca una sesión (cookie HttpOnly)
          ├─ métricas crudas (psutil)
          └─ costo estimado: shared/asterion_shared (misma fórmula que Cloud)
          │
          ▼
   frontend-core (React) — pantalla de login pide pegar el token
```

Puntos clave de diseño:

- **Dos servicios, dos puertos, dos propósitos.** El agente
  (`asterion agent-run`) empuja métricas a Asterion Cloud en segundo plano
  con la api-key de una instancia conectada. `backend-core` es un servidor
  HTTP con login humano (el token) que solo vos podés abrir en el
  navegador — no hablan entre sí ni comparten sesión.
- **backend-core es Python/FastAPI**, no Go — deliberadamente, para poder
  reusar código de verdad con `backend/` (Asterion Cloud), que también es
  FastAPI. El cálculo de costo (`shared/asterion_shared/pricing.py`) es un
  único paquete Python instalado en modo editable por los dos: la fórmula
  nunca puede divergir entre Core y Cloud.
- **El token nunca se guarda en texto plano.** `local-auth.yaml` solo tiene
  el hash SHA-256 — el mismo patrón que ya usan las API keys de instancia.
  Si lo perdés, no hay forma de recuperarlo: `asterion local auth rotate`
  genera uno nuevo (e invalida el anterior).
- **El archivo solo lo puede leer tu usuario del sistema operativo**
  (permisos `0600`) — la seguridad no depende de una cuenta externa, sino
  de quién tiene acceso al filesystem de esta máquina, que es exactamente
  el modelo de amenaza correcto para una herramienta que ya corre en
  localhost.
- **Precios: tabla pública + refresco en vivo.** backend-core trae una
  tabla de precios pública embebida (`backend-core/app/data/default_pricing.json`)
  para poder estimar costo sin conexión a nada. Si hay una sesión de
  `asterion cloud login`, la refresca periódicamente contra `GET /pricing`
  de Asterion Cloud (`PRICING_REFRESH_MINUTES`, default 60) y cachea el
  resultado en disco — funciona offline entre refrescos, y mejor con
  sesión.

### Requisitos

Ya no hace falta nada externo: sin Firebase, sin cuenta de Google, sin
Asterion Cloud, sin MySQL, sin Redis. Solo:

1. **Python 3.10+** (para `backend-core/`).
2. **Node + pnpm** (para compilar `frontend-core/` una vez).

Con eso, `asterion local serve` funciona de punta a punta — ver el detalle
completo (variables de entorno, endpoints) en
[backend-core/README.md](backend-core/README.md) y
[frontend-core/README.md](frontend-core/README.md).

### Instalación (una vez)

`backend-core/venv` se crea solo la primera vez que corrés `asterion local
serve` (crea el venv, instala `requirements.txt`) — no hace falta prepararlo
a mano. Si tu `python3` del PATH es muy nuevo para alguna dependencia
(pydantic-core/PyO3 suele ser la primera en fallar en versiones de Python
recién salidas), pasá `--python /ruta/a/otro/python3` con una versión más
estable. `frontend-core` sí hay que compilarlo aparte, una vez:

```bash
cd asterion-core/frontend-core
pnpm install
pnpm build
```

Config por entorno, en los dos: `.env.development`/`.env.production`
(cargados solos — `APP_ENV` en backend-core, el modo de Vite en
frontend-core — ver "Requisitos" arriba), `.env.example` como plantilla
para armar cualquiera de los dos desde cero.

### Uso

```bash
asterion local serve
# Token de acceso generado: 7f3a9c21...
# Guardado (hasheado) en ~/Library/Application Support/asterion/local-auth.yaml — no se vuelve a mostrar.
# Abrí http://127.0.0.1:8091 y pegá ese token.
```

(La ruta exacta depende del SO — `os.UserConfigDir()` de Go: `~/Library/Application
Support/asterion` en macOS, `~/.config/asterion` en Linux, `%AppData%\asterion` en
Windows. El mensaje siempre imprime la real, nunca una asumida — `backend-core`
la resuelve con el mismo criterio, ver `app/config.py`, para que ambos lados
miren siempre el mismo archivo.)

La primera vez imprime el token — copialo, no queda en ningún otro lado en
texto plano. Las siguientes veces reusa el que ya existe (avisa que lo está
reusando, no lo vuelve a mostrar). Si lo perdiste:

```bash
asterion local auth rotate    # genera uno nuevo, invalida el anterior
asterion local auth status    # si hay uno configurado y desde cuándo (nunca el secreto)
```

Por default `local serve` bloquea la terminal (Ctrl-C para parar). Con
`--background` queda corriendo solo, desvinculado de la sesión (mismo
mecanismo que usan los plugins), y devuelve el control enseguida:

```bash
asterion local serve --background
# ✓ Dashboard local corriendo en segundo plano — http://127.0.0.1:60522 (pid 52021)
#   logs: .../asterion/logs/local-serve.log
#   detenerlo: asterion local stop (o el botón de apagar dentro del dashboard)

asterion local stop
```

También se puede apagar desde el propio dashboard — botón "Apagar
dashboard" junto a "Cerrar sesión", una vez logueado. Los tres caminos
(Ctrl-C en primer plano, `asterion local stop`, el botón del frontend)
terminan en lo mismo: `SIGTERM` al proceso, que uvicorn maneja de forma
prolija.

## Estructura

```
asterion-core/
  cmd/
    asterion/         el CLI (`asterion ...`)
    asterion-core/    el servicio HTTP que sirve los Provider Adapters
  internal/
    capabilities/     qué operaciones puede declarar un proveedor
    adapters/         el contrato ProviderAdapter + Registry + un paquete por proveedor
    coreserver/       la API HTTP de asterion-core
    apiclient/        cliente Go de la API de Asterion (usa el CLI)
    coreclient/       cliente Go de asterion-core (usa el CLI, sin sesión)
    localstore/       inventario local de instancias (~/.config/asterion/instances.json)
    sysinfo/          datos crudos de la máquina (CPU/RAM/disco/red) — sin costo, usado por 'local' y 'agent-run'
    runtime/          Runtime Engine: Environment Discovery, config persistente, doctor, SSH/red/firewall, risk analysis
    safety/           Capability system (detect/inspect/plan/apply/verify/rollback) + firewall plan — solo lectura por ahora
    lab/              Asterion Lab: laboratorios de infraestructura en YAML, VMs QEMU reales (probado macOS/HVF) — ver README § Asterion Lab
    plugins/          plugins de terceros: manifiesto, instalación (git clone), proceso propio, config cifrada — ver README § Plugins
    localauth/        token de acceso a `local serve` (~/.config/asterion/local-auth.yaml) — reemplaza el login con Google
    cliconfig/        config y sesión persistidas en ~/.config/asterion/
  backend-core/       servicio local Python/FastAPI: login con token propio, métricas (psutil), costo estimado
  frontend-core/      dashboard React que consume a backend-core — build servido por él mismo
```

`shared/asterion_shared/` (fuera de `asterion-core/`, en la raíz del repo)
es el paquete Python con la fórmula de costo, compartido entre
`backend-core/` y `backend/` (Asterion Cloud) — ver
[`../shared/README.md`](../shared/README.md).

`go doc ./...` documenta cada paquete; los comentarios de paquete explican
el rol de cada uno con más detalle que este README.

## Compilar

Requiere Go 1.23+ (el `go.mod` fija el toolchain exacto vía `go 1.25.0`,
`go build`/`go run` lo resuelven solos si tenés `GOTOOLCHAIN=auto`, el
default).

```bash
cd asterion-core
go build -o asterion ./cmd/asterion
go build -o asterion-core ./cmd/asterion-core
```

Para instalar `asterion` en tu `$GOPATH/bin` (y poder correrlo desde
cualquier lado si ese directorio está en tu `$PATH`):

```bash
go install ./cmd/asterion
```

## Levantar asterion-core

```bash
./asterion-core                       # escucha en :8090
ASTERION_CORE_ADDR=:9000 ./asterion-core  # puerto custom
```

La API de Asterion (backend Python) apunta a esto con `CORE_SERVICE_URL`
en su `.env` (default `http://localhost:8090`). Si asterion-core no está
corriendo, el backend se degrada con gracia — el aprovisionamiento sigue
funcionando contra el estado propio de Asterion, solo que sin intentar la
llamada real al proveedor (ver `app/services/core_client.py`).

## Modo local (sin sesión)

```bash
asterion instances add --name web-prod --host 10.0.0.5 --user ubuntu
asterion instances list
asterion instances connect web-prod   # abre una sesión SSH real
asterion instances remove web-prod
```

## Asterion Cloud

Login sin contraseña: se manda un código de un solo uso al email de tu
cuenta de Asterion (mismo mecanismo que ya usás para invitaciones de
proyecto). El CLI nunca ve cómo la API arma la sesión internamente — solo
recibe un access token + refresh token genéricos vía `/auth/cli/*`.

```bash
asterion cloud login --api-url https://api.tu-dominio.com --email vos@tuempresa.com
# Te pide el código de 6 dígitos que llegó por correo, y listo.

asterion whoami
asterion cloud logout
```

Vincular una instancia local a un proyecto (sin duplicarla):

```bash
asterion cloud connect web-prod --project 1
# ✓ Instancia local encontrada: web-prod
# ✓ Token de enrolamiento generado
# ✓ Instancia conectada a Asterion Cloud
```

Correrlo de nuevo sobre la misma instancia no crea una segunda fila:
reporta "ya estaba conectada" y reusa la existente.

Registrar la máquina donde corre el CLI como una instancia y dejar el
agente instalado y corriendo:

```bash
asterion cloud install-agent --project 1
```

El resto de la plataforma (cuentas cloud, motor de aprovisionamiento):

```bash
asterion projects list

# Conectar una cuenta cloud (las credenciales se cifran en el backend,
# nunca se guardan ni se muestran en texto plano de nuevo)
asterion cloud-accounts create \
  --project 1 --provider 2 --alias prod-aws --region us-east-1 \
  --cred access_key_id=AKIA... --cred secret_access_key=...

asterion cloud-accounts list --project 1

# Motor de aprovisionamiento: describir -> planificar -> confirmar -> aplicar
asterion provision describe --project 1 --type instance --spec '{
  "cloud_account_id": 1, "shape_id": 2, "image_id": 1,
  "region": "us-east-1", "instance_name": "api-prod", "storage_gb": 40
}'
asterion provision plan 5      # valida, arma el DAG de pasos, estima el costo mensual
asterion provision confirm 5   # aprobás explícitamente el plan estimado
asterion provision apply 5     # recién acá se ejecuta

asterion provision status 5
asterion instances list --project 1   # instancias del proyecto (no las locales)
```

Consultar qué sabe hacer cada proveedor:

```bash
asterion providers
asterion capabilities aws
asterion capabilities oci   # oci todavía no declara "pricing"
```

## Agregar un proveedor nuevo (DigitalOcean, Hetzner, Vultr, Alibaba, IBM...)

1. `internal/adapters/<proveedor>/adapter.go`: implementar `ProviderAdapter`
   (ver `internal/adapters/aws/adapter.go` como plantilla — `Code()`,
   `Capabilities()` con lo que declara soportar, los métodos `Create*` para
   aprovisionar recursos nuevos, y los `List*` para descubrir recursos que
   ya existen en la cuenta).
2. Registrarlo en `cmd/asterion-core/main.go`, agregándolo a
   `adapters.NewRegistry(...)`.
3. Nada más cambia: ni el CLI ni la API de Asterion necesitan tocarse — ya
   hablan con el `Registry` de forma genérica.

## Runtime Engine: `local status` / `local doctor` / `local config`

Distinto del Provisioning Engine (crea infraestructura en un proveedor):
el Runtime Engine (`internal/runtime/`) sabe leer y administrar **esta
máquina**, la que ya existe. Lo usan tanto `asterion local` como
`asterion agent-run` — un solo lugar para "qué hay instalado acá", nunca
duplicado entre el CLI y el agente.

```bash
asterion local status   # qué se detectó (systemd, firewall, reverse proxy, tunnel) + config vigente
asterion local doctor   # chequeo de salud: puerto escuchando, exposición, remote management
asterion local config show
asterion local config get service_port
asterion local config set service_port 9090
```

La detección es de solo lectura (nunca instala ni modifica nada todavía):
mira binarios en PATH y systemd, y para el puerto de la app hace una
conexión TCP real en vez de asumir — si no puede confirmar algo, devuelve
`"unknown"` o una lista vacía, nunca inventa un dato.

`backend-core` (el dashboard local, ver más abajo) no reimplementa nada de
esto: le pide exactamente los mismos comandos al binario `asterion` vía
subproceso (`app/runtime_bridge.py`) y muestra el mismo JSON — un único
Runtime Engine, dos formas de mirarlo (terminal o navegador).

### El Agent: heartbeat separado de las métricas

Además de las métricas cada 60s, `asterion agent-run` ahora manda un
**heartbeat** liviano cada 30s (`POST /agent/heartbeat`, configurable con
`--heartbeat-interval`) — así Asterion Cloud sabe "sigue vivo" sin
depender de que en ese ciclo haya habido métricas nuevas. Cloud calcula
`online`/`stale`/`offline` a partir de cuándo llegó el último heartbeat
(nunca lo guarda como un campo fijo, para que no quede pisado si el
proceso murió a mitad de camino) y lo expone en
`GET /instances/{id}/agent-status` — visible en el panel de cada instancia
en el dashboard de Cloud.

```bash
asterion agent status <nombre-local>       # estado LOCAL: clave guardada, systemd
asterion cloud uninstall-agent <nombre-local> --project <id>   # revoca en Cloud + desinstala el servicio local
```

## Plugins de terceros

Cualquiera puede publicar en GitHub una integración para Asterion sin tocar
este repo. Un plugin es un **proceso separado**, con su propia API HTTP en
su propio puerto — nunca código que se carga adentro del proceso de
`asterion` (nada de `plugin.so`/cgo): así el plugin nunca tiene acceso a lo
que ya está en memoria del CLI (sesión, credenciales cloud descifradas), la
misma frontera de aislamiento que ya separa a dos programas cualquiera del
sistema operativo. La administración del ciclo de vida vive en
`internal/plugins/`; la definición del contrato en sí — qué es un
`plugin.yaml` válido — vive en el repo hermano
[`asterion-plugin-contract`](https://github.com/Tarafagat/asterion-plugin-contract)
(el **Asterion Plugin Contract**, APC), clonado al lado de este repo igual
que `asterion-lab` (`go.mod` lo referencia con `replace`). `internal/plugins.Manifest`
es un alias directo de `apc.Manifest` — hay una sola definición del
contrato en todo el ecosistema.

```bash
# ciclo de vida
asterion plugin install github.com/usuario/asterion-plugin-sii   # clona el repo, valida plugin.yaml
asterion plugin config set sii rut_empresa=76.123.456-7 cert_password=...
asterion plugin start sii     # puerto libre elegido solo, espera el health check
asterion plugin list          # todos los instalados, con estado real (reconciliado contra el pid)
asterion plugin stop sii
asterion plugin remove sii    # para, borra el repo clonado y su config
asterion plugin connect sii --project 1   # lo vincula a un proyecto de Asterion Cloud

# crear un plugin nuevo
asterion plugin init mi-plugin --language go      # scaffold: ya cumple el contrato, compila tal cual
asterion plugin validate ./mi-plugin              # valida plugin.yaml + lo que referencia (openapi.yaml, schemas)
asterion plugin dev ./mi-plugin                   # arranca el plugin y confirma que su API real coincide con lo declarado
asterion plugin from-openapi mi-api.yaml          # infiere un plugin.yaml de partida a partir de una API REST propia
```

Un repo de plugin es cualquier cosa con un `plugin.yaml` en la raíz —
ningún script de instalación arbitrario corre nunca, solo se lee este
manifiesto declarativo. Además de lo mínimo (`name`/`version`/`start`/
`port`/`health_path`/`config_schema`, sin cambios desde siempre), el APC
agrega campos opcionales para describir la API completa del plugin:

```yaml
name: sii
version: "1.0.0"
description: "Integración con el SII (Chile)"
contract_version: "asterion.plugin/v1"
start:
  command: python3
  args: ["-m", "app.main"]
port: 0              # 0 = Asterion elige un puerto libre
health_path: "/health"
config_schema:
  - key: rut_empresa
    label: "RUT de la empresa"
    type: string
    required: true
  - key: cert_password
    label: "Clave del certificado"
    type: string
    secret: true
    required: true
permissions:
  network: ["sii.cl"]
  secrets: true
resources:
  - name: invoices
    endpoint: /invoices
    crud: [create, read, list]
actions:
  - name: issue_invoice
    method: POST
    endpoint: /invoices/{id}/issue
```

Ver la especificación completa, con todos los campos y un plugin de
referencia funcionando (`dummy-fs-provider`), en
[`asterion-plugin-contract/spec/apc-v1.md`](https://github.com/Tarafagat/asterion-plugin-contract/blob/main/spec/apc-v1.md).

`frontend-core` renderiza el formulario de configuración de cada plugin
directamente a partir de `config_schema` — un plugin nuevo no requiere ni
una línea de UI en Asterion Core, y `backend-core` expone un reverse proxy
(`/api/plugins/<nombre>/proxy/*`) hacia la API del plugin para que el
navegador nunca tenga que saber en qué puerto quedó corriendo.

Puntos clave de diseño:

- **Config cifrada, nunca en el cliente.** Cada valor se cifra con
  AES-256-GCM antes de tocar disco (`~/.config/asterion/plugins/config/`,
  clave propia generada la primera vez en `master.key`, igual que
  `local-auth.yaml`). Al arrancar el proceso, la config descifrada se le
  pasa por variables de entorno (`ASTERION_PLUGIN_CONFIG_*`) — nunca queda
  en un archivo que el propio plugin tenga que leer y loguear por error.
  Los campos marcados `secret` en el manifiesto nunca se vuelven a mostrar
  en texto plano, ni siquiera al dueño de la máquina.
- **Plugins privados, gratis por construcción.** `asterion plugin install`
  hace `git clone` — si tu git ya tiene credenciales para un repo privado
  (agente SSH, credential helper), la instalación funciona igual que con
  uno público. No hace falta (ni existe) un mecanismo de auth aparte para
  "plugins privados".
- **Identificador único para Cloud.** Al instalarse, cada plugin recibe un
  `external_ref` (`plg_` + 128 bits de `crypto/rand`) generado una sola vez
  en esta máquina — la misma idea que `inst_xxxxxxxx` para instancias (ver
  más arriba). `asterion plugin connect` lo manda a
  `POST /projects/{id}/plugins/connect-local`: si ese mismo plugin se
  reconecta, Cloud reusa la fila existente en vez de duplicarla. Cloud
  nunca ve el proceso del plugin ni su configuración — solo que existe, su
  nombre/versión, y a qué proyecto pertenece.
- **La instalación entera es portable.** Todo lo de este sistema vive bajo
  `~/.config/asterion/plugins/` (repos clonados, estado, config cifrada,
  la master key) — clonar `~/.config/asterion/` completo a otra máquina
  alcanza para reproducir los plugins instalados ahí, sin un mecanismo de
  export/import aparte.

**Estado real, sin adornos**: el arranque/parada de procesos y la
detección de si siguen vivos usan señal 0 (POSIX) — confirmado en
Linux/macOS; en Windows `asterion plugin status` no puede confirmar el
estado del proceso y lo dice explícitamente en vez de inventarlo (mismo
criterio que `sysinfo`/`safety` en el resto de este README), y el proceso
del plugin queda ligado a la consola porque Windows no tiene `Setsid`. El
APC agrega `permissions` (qué red/filesystem/base de datos/secretos declara
necesitar un plugin) y `contract_version` (Asterion rechaza un manifiesto
que declare una versión de contrato que no reconoce), probado en vivo con
`asterion plugin validate` sobre manifiestos rotos a propósito — pero
`permissions` sigue siendo **declarativo, no forzado**: instalar un plugin
de un tercero sigue siendo, hoy, ejecutar su proceso con los mismos
privilegios que tu usuario del sistema operativo. Forzarlo de verdad
requeriría correr el plugin dentro de un contenedor/VM de Asterion Lab en
vez de como proceso directo — no está hecho todavía. Tampoco hay un índice
curado de plugins más allá del `plugin_catalog` de Asterion Cloud (la
marketplace pública, ver su README) — la mitigación actual sigue siendo el
manifiesto declarativo (nunca se corre nada que no esté ahí) más que el
usuario elige explícitamente qué instalar.

## Infrastructure Safety Lab

La pregunta que motiva todo este sistema: *"si instalo ufw en una instancia
y me bloqueo el SSH a mí mismo, ¿cómo recupero el acceso?"* — la respuesta
de Asterion hoy es que **no puede pasar desde Asterion**, porque **Asterion
todavía no aplica cambios de firewall**. No hay ningún comando `apply`.
Todo lo que existe es de solo lectura: descubre, analiza el riesgo, y te
avisa — nunca toca una regla. Ver `internal/safety/capability.go`:
ningún adapter (`ufw`, `ssh`, `reverse-proxy`, `tunnel`) declara la
capability `apply`, así que `safety.RequireSafeApply()` los rechazaría a
todos igual si algo intentara usarlos para aplicar. La forma de que este
escenario nunca ocurra vía Asterion es, literalmente, no tener todavía el
código que podría causarlo.

### `asterion local doctor` — ahora con SSH y análisis de riesgo

```bash
asterion local doctor
```

Además de lo que ya hacía (puerto de la app, reverse proxy, tunnel,
firewall detectado), ahora descubre de verdad:

- **SSH**: si el servicio está activo, en qué puerto — *nunca asume
  22/tcp*: lee `sshd -T` si está disponible, si no las directivas `Port`
  de `sshd_config`/`sshd_config.d/*.conf`, y solo si no encuentra ninguna
  directiva explícita reporta 22 como el default de OpenSSH documentado
  (marcado como tal, no como algo confirmado) — más sesiones activas
  (`who`) y si el propio proceso de Asterion está corriendo sobre SSH.
- **Red**: interfaces, IPs, ruta por defecto, DNS, puertos escuchando
  (TCP/UDP) — vía `ip`/`ss`, sin dependencias nuevas.
- **Firewall, de verdad**: ya no solo "¿está instalado?" — intenta leer el
  estado real (`ufw status verbose` / `nft list ruleset` / `iptables -L`).
  En Linux esto necesita root; si no lo tiene, el reporte dice
  explícitamente *"no se pudo leer, correr con sudo"* — **nunca** interpreta
  "no pude leer las reglas" como "no hay reglas". Confundir esas dos cosas
  sería exactamente el tipo de falso negativo que causa un bloqueo.
- **Risk Analysis de SSH** (`internal/runtime/risk.go`, probado con tests
  reales en `risk_test.go`): combina lo de arriba en un nivel —
  `low`/`medium`/`high`/`critical`/`unknown`. Ejemplo real: firewall activo
  y sin una regla ALLOW para el puerto SSH detectado → `critical`,
  agravado si la sesión actual de Asterion es esa misma conexión SSH. Si
  no se pudo leer el firewall → `unknown`, nunca `low`.

### `asterion firewall plan` — planificación, nunca aplicación

```bash
asterion firewall plan
```

100% de lectura: junta el descubrimiento de SSH + firewall + reverse proxy
de arriba y arma una propuesta (política por defecto sugerida + qué
puertos habría que exceptuar) — el formato del spec de referencia
("CURRENT STATE / PROPOSED CHANGE / RISK / PROTECTED SERVICES / ROLLBACK").
`rollback_available` y `apply_available` en la salida son siempre `false`
hoy, calculados desde las capabilities reales del `UFWAdapter` — no un
texto fijo, si el día de mañana se agrega `apply` con `rollback`, este
comando lo va a reflejar solo con que el adapter declare esas capabilities.

### Capability System (`internal/safety/`)

Cada adapter de infraestructura local declara qué sabe hacer, igual que ya
hacían los Provider Adapters de nube con `internal/capabilities`:

```bash
asterion local status   # incluye "safety_capabilities" por adapter
```

Hoy los 4 (`ufw`, `ssh`, `reverse-proxy`, `tunnel`) declaran únicamente
`detect`/`inspect`/`plan` — `apply`/`verify`/`rollback` están ausentes del
mapa (no en `false`: ausentes, para que quede explícito en el JSON que ni
se intentaron). La regla dura, en código, no solo en comentario:
`safety.RequireSafeApply()` rechaza cualquier intento de `apply` si el
adapter no declaró también `rollback` — un adapter nunca puede aplicar un
cambio que no sepa deshacer.

### Asterion Lab (`asterion lab ...` / `asterion vm ...` / `asterion container ...` / `asterion images ...`)

Laboratorios de infraestructura reproducibles desde la terminal: VMs QEMU
reales y/o contenedores Docker reales, definidos en el mismo YAML, con
red privada propia — pensado sobre todo para poder probar una regla de
firewall de verdad (instalarla, aplicarla, confirmar que bloquea lo que
tiene que bloquear) o una imagen Docker propia antes de siquiera pensar
en aplicar/publicar eso contra algo real (ver "Infrastructure Safety Lab"
más abajo: esa es justamente la razón de ser de este componente).

**Cómo se conecta con este repo.** Todo lo de arriba se invoca desde acá
mismo — `asterion lab ...` es un comando más del binario `asterion`
(`cmd/asterion/lab.go`, `vm.go`, `container.go`, `images.go`), no un
programa aparte que haya que instalar o correr por separado. Lo que sí
está separado es *dónde vive la lógica*: todo el motor real (QEMU, VDE,
QMP, Docker, YAML, estado) es el módulo Go `asterion-lab`, un repositorio
propio, hermano de este (`asterion-core`) — **tienen que estar clonados
uno al lado del otro** (mismo directorio padre) para que esto compile,
porque `asterion-core/go.mod` lo referencia por ruta relativa
(`replace asterion-lab => ../asterion-lab`, ya que `asterion-lab` no está
publicado en ningún registry de Go). Es la misma idea que ya usa
`asterion-shared` del lado Python entre este repo y `asterion-cloud`: un
módulo compartido, en su propio repositorio, consumido por más de un
proyecto. Ver el README de `asterion-lab` para el detalle de su
arquitectura interna (paquetes `spec`, `store`, `qemu`, `docker`,
`network`, `firewall`, `qmp`, `sshexec`, `image`, `cloudinit`, `testrun`,
`process`, `cmderr`).

**Comandos disponibles, completos:**

```bash
# Laboratorio (crea/arranca/para/destruye VMs y contenedores juntos)
asterion lab create <archivo.yaml>      # provisiona (no arranca)
asterion lab start <nombre>             # arranca todo, espera SSH/Docker, aplica firewall
asterion lab stop <nombre>              # para sin borrar nada
asterion lab destroy <nombre>           # para y borra todo
asterion lab list                       # todos los laboratorios conocidos
asterion lab status <nombre>            # estado real (reconciliado contra procesos/containers)
asterion lab test <nombre>              # corre 'tests:' contra VMs y contenedores
asterion lab run <archivo.yaml>         # create + start + test + destroy en un solo comando

# VMs (backend QEMU)
asterion vm list                        # todas las VMs de todos los laboratorios
asterion vm ssh <lab> <vm>              # sesión interactiva
asterion vm exec <lab> <vm> <cmd>       # un comando puntual, por SSH
asterion vm clone <lab> <vm> <nuevo>    # nueva VM a partir del disco actual — en caliente si sigue corriendo
asterion vm snapshot create/restore/delete/list <lab> <vm> [<nombre>]  # en caliente vía QMP si corre

# Contenedores (backend Docker)
asterion container list                 # todos los contenedores de todos los laboratorios
asterion container exec <lab> <c> <cmd> # un comando puntual, vía docker exec
asterion container logs <lab> <c>       # logs del contenedor

# Catálogo de versiones de imágenes Docker (por usuario, no por laboratorio)
asterion images list                    # imágenes conocidas, con su digest real
asterion images pull <imagen>           # descarga y registra la versión
asterion images remove <imagen>         # borra de Docker y del catálogo
asterion images remove <imagen> --forget-only  # solo saca del catálogo, deja la imagen en Docker
```

```yaml
# firewall-lab.yaml
apiVersion: asterion.dev/v1
kind: Lab
name: firewall-basico
network:
  name: lab-net
  cidr: 10.10.0.0/24
vms:
  - name: server
    image: ubuntu-24.04
    ip: 10.10.0.10
    firewall:
      backend: ufw
      rules:
        - {action: allow, port: 22}
        - {action: deny, port: 8080}
  - name: client
    image: alpine-3.20
    ip: 10.10.0.11
tests:
  - name: "22 sigue permitido"
    vm: client
    run: "nc -zvw3 10.10.0.10 22"
    expect: {exit_code: 0}
  - name: "8080 queda bloqueado"
    vm: client
    run: "nc -zvw3 10.10.0.10 8080"
    expect: {exit_code: 1}
```

```bash
asterion lab create firewall-lab.yaml   # provisiona los discos + cloud-init (no arranca)
asterion lab start firewall-basico      # arranca las VMs, espera SSH, aplica ufw
asterion lab test firewall-basico       # corre 'tests:' y compara contra 'expect'
asterion lab destroy firewall-basico    # para y borra todo — discos, seeds, logs, clave SSH

asterion lab run firewall-lab.yaml      # create + start + test + destroy en un solo comando

asterion vm ssh firewall-basico server        # sesión interactiva
asterion vm exec firewall-basico server "..."  # un comando puntual
asterion vm clone firewall-basico server backup  # nueva VM a partir del disco actual (backing file, instantáneo)
asterion vm snapshot create firewall-basico server antes-de-x
```

**Estado real, probado en vivo (no en teoría)**: contra macOS/arm64 con
HVF, de punta a punta — boot de Ubuntu 24.04 vía cloud-init en ~9s, disco
overlay copy-on-write sobre la imagen base (nunca se reinstala el SO), red
compartida entre cualquier cantidad de VMs vía un switch VDE propio (ver
más abajo — probado con 3), `ufw` instalado y habilitado por SSH de
verdad, y confirmado que el puerto bloqueado da timeout mientras el
permitido sigue abierto, incluso con 3 VMs en la misma red. El ejemplo de
arriba es literal — es el YAML que se corrió para probar esto, no uno
ilustrativo.

**Es su propio módulo Go** (`asterion-lab/`, carpeta hermana de
`asterion-core/`, no un paquete interno) — la razón es puramente de
escala: red, firewall, snapshots, imágenes y orquestación de VMs son,
cada una, suficiente superficie como para merecer su propio paquete en
vez de vivir todas juntas en `internal/lab`. `asterion-core/go.mod` lo
referencia por ruta local (`replace asterion-lab => ../asterion-lab`) —
mismo criterio que `asterion-shared` del lado Python (editable install
compartido entre `asterion-core/backend-core` y `asterion-cloud/backend`).
El CLI (`cmd/asterion/lab.go`, `cmd/asterion/vm.go`) lo importa como
cualquier otra dependencia — los comandos (`asterion lab ...`,
`asterion vm ...`) no cambiaron.

Dentro de `asterion-lab/`, cada paquete tiene una sola responsabilidad y
sin dependencias circulares: `spec` (el YAML — VMs y contenedores, sin
saber nada de QEMU ni Docker), `store` (estado persistido en
`~/.config/asterion/lab/labs/<id>/` — autocontenido, clonar
`~/.config/asterion/` alcanza para llevarse los laboratorios a otra
máquina), `image` (catálogo de imágenes base QEMU + caché en
`~/.config/asterion/lab/images/`), `cloudinit` (arma el seed — build tags
por SO: `hdiutil` en macOS, `genisoimage`/`mkisofs`/`xorriso` en Linux),
`network` (puertos, MACs, y el switch VDE compartido del laboratorio, ver
abajo), `qmp` (cliente QMP mínimo, sin dependencias de terceros, sobre el
socket unix del monitor de cada VM), `sshexec` (ejecutar comandos reales
en una VM, shell-out a `ssh` — mismo criterio que el resto del CLI: nunca
una librería nueva si el binario del sistema alcanza), `firewall` (aplica
`ufw` de verdad por SSH), `qemu` (construye y arranca el proceso QEMU,
build tags por SO para aceleración y firmware UEFI), **`docker`** (backend
de contenedores: shell-out a `docker`, sin SDK — pull con digest
resuelto, create/start/stop/exec/logs/rm, y el catálogo local de
versiones de imágenes), `process` (señales/detach de procesos del SO),
`cmderr` (errores de comandos externos con su salida real), `testrun`
(corre las aserciones de `tests:`, resolviendo el target contra VMs o
contenedores indistintamente). El paquete raíz (`lab.go`, `orchestrate.go`,
`clone.go`, `types.go`) es la capa de ensamblado: expone `CreateLab`/
`StartLab`/`StopLab`/`DestroyLab`/`CloneVM`/`SnapshotVM`/`ContainerExec`/
`ListDockerImages`/... como API pública — type aliases (`type LabState =
store.LabState`, etc.) para que quien lo importa siga viendo
`lab.LabState`, `lab.VMSpec`, `lab.ContainerSpec`, sin necesitar saber que
por dentro está organizado en subpaquetes.

**Backend Docker, probado en vivo**: instalé Colima (Docker Engine
Community corriendo en una VM liviana, sin Docker Desktop) específicamente
para poder probar esto contra un daemon real, mismo criterio que QEMU/VDE.
Probado: laboratorio solo de contenedores (nginx, con el puerto publicado
accesible desde el host de verdad), y un laboratorio **mixto** — una VM
QEMU (Alpine) y un contenedor Docker (Redis) al mismo tiempo, con
`asterion lab test` resolviendo cada aserción contra el backend que
corresponde según el nombre, sin que el YAML tenga que decir cuál es
cuál. El catálogo de versiones de imágenes (`asterion images ...`)
también se probó de punta a punta: pull con digest real capturado,
listado, "olvidar" sin borrar de Docker, y borrado real (`docker rmi`) —
confirmado con `docker images` antes y después de cada operación.

**Red de más de 2 VMs (VDE)**: la primera versión usaba `-netdev socket`
de QEMU (enlace punto a punto, un solo `listen` no acepta más de un peer)
y después se probó `-netdev socket,mcast=` (multicast UDP) como
reemplazo — en el papel es el mecanismo "estándar" de QEMU para esto, pero
en esta Mac nunca entregó un solo paquete entre dos procesos QEMU: se
confirmó con un sniffer UDP independiente, joineado exactamente al mismo
grupo/puerto que `lsof` mostraba bindeado en los procesos QEMU, escuchando
durante 8 pings consecutivos del guest — cero paquetes recibidos. Se
descartó por no funcionar, no por preferencia. La solución real es
`-netdev vde,sock=<dir>`: Asterion levanta su propio `vde_switch` (switch
de software VDE, un puerto por VM, sin bridge ni root) por laboratorio,
uno por cada `asterion lab start`, y lo apaga en `stop`/`destroy`. Probado
en vivo con 3 VMs (1 Ubuntu + 2 Alpine): ping entre las tres, conexión TCP
directa entre dos VMs cliente (no solo hacia el server), y una regla `ufw`
real bloqueando/permitiendo el puerto correcto en las tres a la vez.
Requiere el binario `vde_switch` (`brew install vde` en macOS, `apt
install vde2` en Debian/Ubuntu) — se comprueba antes de crear cualquier
laboratorio de 2+ VMs, mismo criterio que `QEMUAvailable()`.

**Snapshots y clones en caliente (QMP)**: cada VM arranca con su propio
monitor QMP en un socket unix (`-qmp unix:<path>,server=on,wait=off`,
reemplazó el `-monitor none` sin reemplazo que había antes). El paquete
`qmp` es un cliente QMP mínimo hecho a mano (JSON por líneas sobre el
socket, sin librería de terceros — todo el módulo asterion-lab solo
depende de yaml.v3). El socket vive en `/tmp` (`qmp.NewSocketPath`), no
bajo `~/Library/Application Support/asterion/...` como el resto del
estado del laboratorio — encontrado en vivo: un socket unix tiene un
límite de ~104 bytes en macOS para su path completo, y anidarlo bajo el
directorio de datos (`.../lab/labs/<id>/vms/<nombre>/qmp.sock`) se pasaba
de eso apenas el nombre de la VM no era trivial — clonar una VM con un
nombre un poco largo fallaba con "SSH nunca respondió" sin ningún otro
indicio, porque QEMU ni siquiera llegaba a arrancar ("UNIX socket path
... is too long", visible solo corriendo `qemu-system-*` a mano con
`stderr` capturado). Mismo motivo por el que Docker, ssh-agent y Postgres
ponen sus sockets en `/tmp` en vez del directorio de datos del usuario.
Con la VM corriendo:
- `vm snapshot create/restore/delete` usan `savevm`/`loadvm`/`delvm` del
  monitor HMP clásico vía `human-monitor-command` — capturan CPU + RAM +
  disco completos, sin apagar la VM. Probado en vivo: marcar un archivo,
  snapshot, cambiar el archivo, restaurar, confirmar que volvió al valor
  original y que el `uptime` se reinició (la VM realmente volvió a ese
  instante).
- `vm clone` de una VM corriendo usa QMP `blockdev-snapshot-sync` para
  congelar su disco actual (la VM sigue escribiendo a un overlay nuevo
  desde ese instante, sin pausar) y clona a partir del archivo ya
  congelado. Antes de congelar corre `sync` por SSH dentro del guest —
  necesario porque `blockdev-snapshot-sync` congela el disco pero no la
  RAM: sin el sync, una escritura reciente que todavía estuviera solo en
  el page cache del guest no aparecía en el clon (encontrado en vivo,
  reproducido y confirmado antes de agregar el sync automático). No es
  consistencia de aplicación completa (para eso haría falta
  `qemu-guest-agent` con `fsfreeze`, no incluido), pero cubre el caso
  común sin esa dependencia extra. Probado en vivo con 4 clones sucesivos
  de la misma VM corriendo, cada uno independiente y con el estado
  correcto congelado en su momento, sin afectar nunca al origen.

Con la VM detenida, ambas operaciones siguen usando `qemu-img` directo
sobre el archivo (más simple, no necesita que el proceso esté vivo) — el
mismo comando (`vm snapshot create`, `vm clone`) funciona en los dos
casos, Asterion decide internamente cuál camino tomar.

**Qué falta, con precisión (para poder compilar/probar los demás
adapters)**:

- **Linux nativo (KVM)**: el código está escrito (`qemu_linux.go`:
  aceleración `kvm`, detección de firmware AAVMF/OVMF por las rutas de
  Debian/Ubuntu/Fedora) pero nunca se corrió contra una máquina Linux real
  — falta esa verificación. Necesita `qemu-system-{aarch64,x86_64}`,
  `/dev/kvm` accesible, `vde2` para redes de 2+ VMs, y
  `genisoimage`/`mkisofs`/`xorriso` instalado para el seed de cloud-init
  (macOS no necesita esto porque usa `hdiutil`, Linux no trae un
  equivalente por default).
- **Windows (WHPX)**: `qemu_windows.go` declara el flag de aceleración
  correcto pero `findFirmware` devuelve `ErrNotImplemented` — no se probó
  ninguna ruta de instalación de QEMU en Windows, y `cloudinit_windows.go`
  tampoco arma el seed todavía (no hay equivalente nativo a `hdiutil` sin
  pedir una herramienta aparte — candidatos: `oscdimg` del Windows ADK, o
  `mkisofs`/`xorriso` vía WSL/Chocolatey, ninguno evaluado). VDE también
  tiene build para Windows pero no se probó ahí.
- **Backends más allá de QEMU y Docker** (Hyper-V, Apple Virtualization
  Framework, o backends cloud vía adapters — AWS/Azure/GCP/OCI): el mismo
  YAML de laboratorio ejecutándose contra infraestructura remota es la
  visión a largo plazo del spec, pero hoy todo asume procesos/contenedores
  locales — llevar esto a un backend remoto es un cambio de diseño grande,
  no una extensión chica.
- **Docker en Linux/Windows**: el backend Docker (`docker/docker.go`) no
  tiene ninguna pieza específica de sistema operativo — todo pasa por el
  binario `docker`, así que en principio debería funcionar igual en
  Linux/Windows con Docker instalado ahí, pero solo se probó en macOS
  (contra Colima).
- **Imágenes base QEMU**: el catálogo (`image/image.go`) hoy tiene 3
  entradas (`ubuntu-24.04`, `ubuntu-22.04`, `alpine-3.20`) para arm64/amd64
  — agregar una imagen nueva es una entrada más en `knownImages`, sin
  tocar el resto.

### Multi-nube: firewall del proveedor vs. firewall del sistema operativo

El spec de referencia también pide distinguir `Provider Network Policy`
(Security Lists/NSGs de OCI, Security Groups de AWS, NSG de Azure,
firewall de GCP) de `Host Firewall` (ufw/nftables/iptables) y
`Service Listener` (qué puerto abre la propia aplicación) — UFW nunca es
"toda la conectividad". Hoy `internal/safety` solo modela la capa de
Host Firewall; la capa de Provider Network Policy requeriría llamar a la
API real de cada proveedor con credenciales reales, exactamente la misma
limitación que ya tienen los Provider Adapters de aprovisionamiento (ver
"Estado actual" más abajo) — se documenta como dependencia explícita, no
se simula.

## Estado actual (honesto)

Los 4 adapters (AWS/Azure/GCP/OCI) declaran capabilities reales y están
100% cableados end-to-end (CLI → API de Asterion → asterion-core →
adapter), pero ninguno llama todavía al SDK real del proveedor — cada
método `Create*` y `List*` (descubrimiento de recursos existentes, ver
capability `Discovery`) devuelve `adapters.ErrNotImplemented`. No hay
credenciales reales de ningún proveedor disponibles para probar esa
integración de punta a punta, y publicar una llamada real sin poder
probarla es peor que no tenerla. El contrato (`ProviderAdapter`, specs,
capabilities) ya está listo para que esa implementación se agregue adapter
por adapter sin tocar nada del resto del sistema.

`asterion local` y `agent-run` (internal/sysinfo) solo leen datos reales en
Linux (/proc, /sys) — en otros sistemas operativos devuelven un error claro
en vez de un dato inventado. La detección física/VM/contenedor es una
heurística de mejor esfuerzo (mira /.dockerenv, /proc/1/cgroup y DMI): si
no puede confirmarlo, devuelve "unknown" en vez de adivinar.

`asterion cloud install-agent` solo instala el servicio automáticamente en
Linux (systemd --user); en otros sistemas operativos te muestra el comando
para correrlo manualmente. El enrolamiento en sentido inverso — una
instancia que nace en Cloud (creada por el motor de aprovisionamiento o
desde la web) y después instala el agente en una máquina nueva — todavía
no tiene comando propio; hoy el flujo cableado es local → cloud.

`backend-core`/`frontend-core` (el dashboard local) están probados de
punta a punta: login gateado por email, métricas reales vía `psutil`,
costo estimado con la tabla pública embebida, y `asterion local serve`
levantando el proceso real y sirviendo el build. Lo que falta: el refresco
de precios en vivo (`GET /pricing` de Cloud) solo se probó con la llamada
HTTP en aislado, no con una sesión real de `asterion cloud login` de punta
a punta contra un backend de Cloud corriendo; y `asterion local serve` no
instala `backend-core` como servicio de fondo (a diferencia de
`install-agent`) — hoy es un proceso en primer plano que corrés cuando lo
necesitás.

**Runtime Engine (`internal/runtime/`) y heartbeat del Agent** — probados
de punta a punta contra la máquina real (detectó de verdad systemd/ufw/
iptables/nftables instalados acá) y contra la base de datos real
(`agent_status`, umbrales online/stale/offline verificados con timestamps
reales). Es, a propósito, **solo la mitad del diseño del prompt de
referencia** ("ASTERION RUNTIME + AGENT"): la parte de **detección** está
completa (Environment Discovery, doctor, config persistente, heartbeat,
panel de Agent en Cloud). Lo que ese mismo documento pide y **no** está
construido todavía — deliberadamente, no por descuido:

- **Adapters que modifican de verdad** el firewall, el reverse proxy
  (Nginx/Caddy/Apache), el tunnel (Cloudflare/Tailscale) o TLS de la
  máquina del usuario. Hoy el Runtime Engine solo *detecta* qué hay
  instalado — aplicar cambios reales sobre la infraestructura de red de
  alguien sin haber probado esos adapters de punta a punta sería peor que
  no tenerlos, igual que pasa con los Provider Adapters de nube.
- **`asterion local repair`/`rollback`**: no tienen sentido sin que exista
  antes algo que Asterion haya aplicado — son la fase siguiente a los
  adapters de arriba.
- **Protocolo de administración remota** (Cloud → revisión de
  configuración firmada → Agent → Runtime Engine → aplicar → verificar,
  spec §31-37): el Agent hoy es de solo lectura hacia Cloud (métricas +
  heartbeat), nunca ejecuta nada que Cloud le mande. `remote_management`
  en `runtime.json` existe como interruptor y permisos granulares, pero
  todavía no hay ningún canal que los use — es intencional: reservar el
  campo antes de tener el protocolo evita tener que migrar configuración
  después.
- **Multi-usuario del dashboard local** (Owner/Admin/Operator/Viewer con
  sesiones propias): `local serve` sigue siendo de un solo usuario, gateado
  al email de `asterion cloud login` — construir RBAC local es un cambio
  de diseño grande sobre `backend-core/app/auth.py`, no una extensión
  chica.
- **Desired State vs Actual State con drift detection**: no hay nada que
  reconciliar todavía porque Cloud no aplica configuración remota (ver
  arriba).

## Licencia

Apache License 2.0 — texto completo en [LICENSE](LICENSE).

Todo lo que vive en este repo (CLI, Remote Agent, motor de Provider
Adapters, sistema de plugins, `backend-core`/`frontend-core`) es
permisivo a propósito: cuanto más fácil sea confiar en el Agent y
construir sobre el SDK de plugins, más rápido crece el ecosistema — no
hay nada acá que Asterion necesite proteger de una reutilización comercial
de terceros.

Asterion Cloud (repo `asterion-cloud`) usa una licencia distinta, AGPLv3,
justamente porque ahí sí hay algo que proteger: sin esa cláusula, cualquiera
podría tomar el control plane, ofrecerlo como servicio a sus propios
clientes, y competir con Asterion Cloud sin devolver nada al proyecto —
como le pasó a MongoDB con AWS antes de que existiera SSPL. AGPL no le pone
techo a nadie que se instale Asterion Cloud para uso interno propio (eso
sigue siendo gratis y sin límites, ver "Self-hosting" arriba); el
requisito de compartir el código modificado solo se activa si se lo ofrece
como servicio a usuarios externos — que es exactamente el escenario que se
quiere desalentar.
