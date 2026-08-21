# Asterion Core

**Open source.** Asterion Core es todo lo que no depende de facturarle a un
cliente: el CLI, el motor de Provider Adapters (Go), y el dashboard local
(`backend-core` + `frontend-core`) con visualización de métricas y cálculo
de costo *estimado*. Asterion Cloud (`backend/` + `frontend/`, closed
source en la parte de facturación) es el único lugar donde ese costo se
convierte en una factura real a un cliente — Core nunca cobra ni factura a
nadie, solo estima y muestra.

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

```bash
cd asterion-core/backend-core
python3 -m venv venv && venv/bin/pip install -r requirements.txt

cd ../frontend-core
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
# Guardado (hasheado) en ~/.config/asterion/local-auth.yaml — no se vuelve a mostrar.
# Abrí http://127.0.0.1:8091 y pegá ese token.
```

La primera vez imprime el token — copialo, no queda en ningún otro lado en
texto plano. Las siguientes veces reusa el que ya existe (avisa que lo está
reusando, no lo vuelve a mostrar). Si lo perdiste:

```bash
asterion local auth rotate    # genera uno nuevo, invalida el anterior
asterion local auth status    # si hay uno configurado y desde cuándo (nunca el secreto)
```

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
    lab/              Infrastructure Safety Lab: interfaz de entornos desechables (Backend, Environment) — sin backend disponible todavía
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

### El laboratorio en sí (`asterion lab ...`)

```bash
asterion lab status              # qué backend de VM/contenedor desechable hay disponible
asterion lab create ubuntu-nginx # crear un entorno desechable
asterion lab test --all          # correr todos los escenarios del laboratorio
```

**Estado real, sin adornos**: en la máquina donde se escribió esto no hay
ningún backend de virtualización utilizable (sin Docker, sin
`qemu-system-x86_64` instalado, sin root sin contraseña para instalarlo) —
`asterion lab status` lo confirma con el motivo real de cada uno. Por eso
`internal/lab/` tiene la interfaz completa (`Backend`, `Environment`,
`Spec`, el flujo create→provision→test→destroy) y dos implementaciones
candidatas (`docker.go`, `qemu.go`) cuyo único método real es
`Available()` — el resto devuelve `ErrNotImplemented`, el mismo patrón que
ya usan los Provider Adapters de nube (`adapters.ErrNotImplemented`) para
lo que todavía no se implementó de verdad. `lab create`/`test`/`run`
fallan con un error claro en vez de simular que crearon una VM: escribir
esa simulación habría sido más rápido, pero es exactamente el tipo de mock
que este componente existe para no ser.

**Qué falta para que esto corra de verdad**: instalar Docker o
`qemu-system-x86_64` + acceso a `/dev/kvm`, implementar `Create`/`List`/
`Exec`/`Destroy` de al menos un backend, y recién ahí escribir los
escenarios de prueba (SSH en puerto no estándar, UFW allow/deny/rollback,
failure injection) que el spec de referencia describe — todo eso es la
fase siguiente, no está simulado acá.

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
