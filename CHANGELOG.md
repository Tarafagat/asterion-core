# Changelog — Asterion Core

Formato basado en [Keep a Changelog](https://keepachangelog.com/es-ES/1.1.0/).
Este proyecto todavía no tiene releases etiquetados en git — las
versiones de acá abajo documentan el estado real del código a medida que
se construye, no tags publicados. Cuando exista el primer release
etiquetado, `Unreleased` pasa a ser `0.1.0`.

## [Unreleased]

### Added
- **Sistema de plugins de terceros**: `asterion plugin install/config/
  start/stop/list/remove/connect` — cada plugin corre como proceso propio
  en un puerto libre elegido automáticamente, con configuración cifrada
  local y un identificador único para que Asterion Cloud pueda verlo
  conectado a un proyecto.
- **Asterion Plugin Contract (APC) v1**: el manifiesto `plugin.yaml` crece
  con campos opcionales para describir la API completa de un plugin
  (`contract_version`, `language`, `api`, `permissions` declarados,
  `resources`, `actions`, `events`) — retrocompatible con todo manifiesto
  existente. La definición canónica vive en el repo hermano
  `asterion-plugin-contract` (`internal/plugins.Manifest` es ahora un alias
  de `apc.Manifest`, sin duplicar la definición). Nuevos comandos:
  `asterion plugin init --language go` (scaffolding real, con Plugin
  Development Kit para Go), `asterion plugin validate` (valida estructura +
  archivos referenciados), `asterion plugin dev` (sandbox local: arranca el
  plugin y confirma que su API real coincide con lo declarado, sin llamar
  nunca operaciones destructivas), `asterion plugin from-openapi`
  (transforma una API REST propia en un `plugin.yaml` de partida),
  `asterion plugin from-ast` (compila un manifiesto declarado explícito en
  Asterion Language — `Contract.define/config/resource/action/...` — a un
  `plugin.yaml`, y lo valida; alternativa sin heurística a `from-openapi`,
  implementada en el paquete `pluginmanifest` del repo hermano
  `asterion-language`). Incluye un plugin de referencia funcionando
  (`dummy-fs-provider`) con tests reales.
- `asterion plugin install --link <carpeta>`: registra un plugin privado
  desde una carpeta local tal cual, sin `git clone` ni copiarla a ningún
  lado — para desarrollo/pruebas sin necesitar ni siquiera un repo git.
  Nuevo campo `linked` en el registro de un plugin instalado; `asterion
  plugin remove` lo respeta y nunca borra la carpeta de un plugin `--link`
  (solo la copia en `~/.config/asterion/plugins/repos/` de un plugin
  instalado normal).
- **Dashboard: selector de carpeta para instalar un plugin `--link`.** La
  pestaña "Desde carpeta local" del panel de Plugins navega el disco de
  esta máquina vía un endpoint nuevo de solo lectura,
  `GET /api/plugins/browse-dirs` (no pasa por el binario `asterion`, es
  filesystem puro en `backend-core`) — necesario porque la File API
  estándar del navegador nunca expone la ruta absoluta real de una
  carpeta fuera de Electron, así que un `<input type="file">` no alcanza
  para esto. Marca con un ✓ las carpetas que ya tienen un `plugin.yaml`
  válido; "Vincular esta carpeta" llama a `POST /api/plugins/install` con
  `link: true`, mismo camino que `asterion plugin install --link` desde
  la terminal.
- **Soporte para macOS**: `asterion local info`/`stats` (CPU/RAM/disco/red
  reales vía `sysctl`, `vm_stat`, `netstat`), `asterion agent install`
  sobre `launchd` (en vez de systemd), detección de firewall
  (Application Firewall + `pf`) además de Linux (ufw/nftables/iptables).
- **Asterion Lab** (`asterion lab ...`, `asterion vm ...`,
  `asterion container ...`, `asterion images ...`): laboratorios de
  infraestructura reproducibles — VMs QEMU y/o contenedores Docker
  desechables definidos en el mismo YAML (un laboratorio puede mezclar
  ambos), con red privada compartida entre cualquier cantidad de VMs
  (switch VDE propio por laboratorio), reglas de firewall reales
  aplicadas y verificadas por SSH, snapshots/clones de VMs en caliente vía
  QMP sin apagarlas, y un catálogo local de versiones de imágenes Docker
  (digest real, no solo el tag). Extraído a su propio módulo Go,
  `asterion-lab` — clonado como repo hermano de este (ver su propio
  changelog y su README para el detalle de cómo se conecta con este CLI).
- Chequeo de detección de firewall multiplataforma para `asterion firewall
  plan`/`local doctor` (solo lectura, nunca aplica cambios reales — esa
  capacidad espera a que exista un lugar seguro para probarla primero,
  que es justamente lo que resolvió Asterion Lab).
- **`asterion local restart`**: para y vuelve a levantar el dashboard local
  en segundo plano en un solo comando, reusando el puerto si estaba
  corriendo — antes había que acordarse de encadenar `local stop` +
  `local serve --background` a mano.
- **Dashboard: navegación por pestañas.** El dashboard local pasa de una
  sola pantalla larga a un `<nav>` con pestañas ("Resumen"/"Plugins", esta
  última con un contador `corriendo/total`) y los controles de sesión
  (cerrar sesión, apagar) movidos a esa misma barra — mismo look and feel
  que la barra superior de Asterion Cloud, para ir acoplando ambos
  dashboards visualmente.
- **Dashboard: panel dedicado por plugin.** Cada plugin instalado tiene un
  botón "Abrir panel" que entra a una vista propia con su dashboard
  embebido de verdad (vía el reverse proxy existente, ahora sirviendo el
  frontend completo del plugin, no solo su API), más tarjetas de
  Características (metadata del manifiesto + la dirección real
  `127.0.0.1:<puerto>` donde corre su API), Configuración, Endpoints
  (catálogo de solo lectura: método/ruta/estructura de cada
  resource/action) y conexión a Asterion Cloud — todo lo que antes vivía
  amontonado en la tarjeta de la lista, ahora movido ahí para que la lista
  misma quede chica y escaneable. Si el plugin no tiene ningún frontend
  propio, el panel igual muestra algo real: el Asterion Plugin Contract le
  genera uno a partir de su `plugin.yaml` (ver `pdk.MountFrontend` en
  `asterion-plugin-contract`).

### Changed
- Sacado (a favor de la tabla de Endpoints de solo lectura, más arriba): el
  tester interactivo que corría `list`/`create`/acciones en vivo contra el
  proxy de un plugin desde el propio dashboard. Con el panel embebido real
  ya cubriendo "usar el plugin", ese tester genérico quedaba redundante —
  la forma real de usar un plugin es su propio dashboard, no un botón
  genérico que dispara un `POST` a ciegas.
- Licencia: Apache License 2.0 (ver [LICENSE](LICENSE)) — explícita a
  propósito, para maximizar la adopción del ecosistema de plugins. Ver
  la sección "Licencia" del README para el porqué de la separación
  respecto a la licencia de Asterion Cloud (AGPLv3).
- `asterion local serve` ya no requiere preparar `backend-core/venv` a
  mano: si no existe, lo crea e instala `requirements.txt` solo (antes
  avisaba y caía silenciosamente al `python3` del sistema, que casi nunca
  tiene `fastapi` instalado). Nuevo flag `--python` para elegir qué
  intérprete usar al crearlo, por si el `python3` del PATH es demasiado
  nuevo para alguna dependencia pineada.
- `asterion local serve --background`: lo deja corriendo desvinculado de la
  sesión (mismo mecanismo que los plugins) en vez de bloquear la terminal.
  Nuevo `asterion local stop`, y un botón "Apagar dashboard" en el propio
  frontend (`POST /api/local/shutdown`) — los tres caminos de apagado
  (Ctrl-C, `local stop`, el botón) mandan la misma señal al mismo proceso.
- **Rate limit de login** en `POST /api/auth/token`: por IP (5 intentos
  cada 5 minutos, configurable), con bloqueo creciente recursivo ante
  insistencia repetida, límite global agregado como defensa contra alguien
  rotando de IP, y lista blanca opcional de IPs/CIDR (`LOGIN_ALLOWED_IPS`).
  Ver `.env.example` y `backend-core/app/auth.py`.
- **Dashboard: usar un plugin, no solo administrarlo.** Si `plugin.yaml`
  declara `resources`/`actions` (Asterion Plugin Contract), el dashboard
  muestra botones genéricos para listar/crear resources y ejecutar actions
  — leído directo del manifiesto, sin código específico de ningún plugin.
- **`asterion language check`**: primera integración del repo hermano
  `asterion-language` — lexer/parser/semantic analyzer reales para la
  capa declarativa del ecosistema. Valida capabilities contra el servicio
  real de adapters cuando está corriendo (vía `internal/coreclient`), con
  un fallback estático explícito cuando no. `plan`/`apply` no existen
  todavía — bloqueados por los Provider Adapters (son stubs) y por no
  haber un DAG reutilizable en Go, no por el diseño del lenguaje.

### Fixed
- **Bug real de login**: `backend-core` (Python) asumía `~/.config/asterion`
  a mano para encontrar `local-auth.yaml`/`credentials.json`, pero el CLI
  (Go) los escribe con `os.UserConfigDir()` — que en macOS es
  `~/Library/Application Support/asterion`, no `~/.config/asterion`. El
  resultado: login rechazado con "no se generó un token" aunque sí existía,
  y el refresh de precios desde Cloud silenciosamente sin credenciales, en
  cualquier instalación de macOS o Windows. `app/config.py` ahora replica la
  resolución exacta de `os.UserConfigDir()` de Go.
- `asterion local serve --background` elegía un puerto puramente al azar
  del sistema operativo, desacoplado del `service_port` (8091 por default)
  que `asterion local doctor`/`status` ya esperaban — el resultado era un
  dashboard corriendo de verdad pero `doctor` reportándolo caído ("nada
  responde en 127.0.0.1:8091"). Ahora el modo background prueba primero
  8091 (y hasta 20 puertos siguientes) antes de resignarse a uno al azar —
  mismo criterio que ya usaba `find_free_port()` en Python para el modo en
  primer plano — y `doctor`/`status` además usan el puerto real de una
  instancia en segundo plano si hay una corriendo, en vez de solo el
  declarado.
- **`asterion plugin start` arrancaba siempre con el manifest de cuando se
  instaló**, no el que hay ahora en disco — `state.json` guarda una foto
  de `plugin.yaml` tomada en `install`/`install --link`, y nada la
  refrescaba después. Un plugin que evolucionara su `plugin.yaml` (nuevos
  `resources`/`actions`, típicamente vía `from-ast --force`) mostraba en
  el dashboard y en `plugin list` datos permanentemente desactualizados
  hasta reinstalarlo. `Start()` ahora relee el manifest desde
  `installed.Dir` antes de arrancar el proceso (si la lectura falla, sigue
  con el último manifest válido conocido en vez de bloquear el arranque)
  — encontrado y corregido mientras se probaba en vivo la tabla de
  Endpoints del panel de plugin, contra `asterion-mail-plugin-basic`
  después de agregarle el resource `recipient-groups`.
