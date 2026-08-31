# Changelog — Asterion Core

Formato basado en [Keep a Changelog](https://keepachangelog.com/es-ES/1.1.0/).
Este proyecto todavía no tiene releases etiquetados en git — las
versiones de acá abajo documentan el estado real del código a medida que
se construye, no tags publicados. Cuando exista el primer release
etiquetado, `Unreleased` pasa a ser `0.1.0`.

## [Unreleased]

### Fixed
- **`asterion plugin build` fallaba en una instancia recién clonada —
  mismo problema que el de `make install` de acá abajo, un nivel más
  adentro.** Reporte en vivo del usuario, en el mismo `fuelity-bot`:
  después de arreglar `make install`, instaló un plugin oficial
  (`asterion plugin install .../asterion-firewall-analysis`) y
  `asterion plugin build asterion-firewall-analysis` falló con
  `replacement directory ../asterion-plugin-contract does not exist`.
  Causa real: el propio `go.mod` de CADA plugin en Go declara `replace
  .../asterion-plugin-contract => ../asterion-plugin-contract` (por
  implementar justamente ese contrato) — pero esa carpeta hermana nunca
  se clonaba en `~/.config/asterion/plugins/repos/`, a diferencia de
  los repos hermanos de `asterion-core` mismo, que `asterion install
  prerequirements` ya resuelve (ver más abajo). Pedido explícito del
  usuario: una sola copia compartida en esa carpeta (no una por
  plugin — todos los plugins viven directamente bajo
  `plugins/repos/`, así que `../` desde cualquiera de ellos ya apunta
  ahí), clonada sola al instalar/compilar un plugin, y actualizada
  junto con el resto al correr `plugin update --all`.
  - `internal/plugins/contract.go` (nuevo): `EnsureContractRepo`
    (clona `asterion-plugin-contract` en `plugins/repos/` si falta, o
    la repara si quedó con un `.git` roto de un clone interrumpido —
    mismo chequeo `git rev-parse HEAD` que ya usa `internal/prereqs`,
    no un simple `.git`) y `UpdateContractRepo` (`git pull --ff-only`,
    solo si ya existe).
  - `internal/plugins.Build` ahora llama `EnsureContractRepo` antes de
    `go build` — es el punto donde de verdad hace falta (sin la
    carpeta, ni siquiera compila), así que ahí el fallo es un error
    real y claro, no silencioso.
  - `asterion plugin install` también la deja lista de una, mejor
    esfuerzo (si falla acá por lo que sea, no corta un install que ya
    terminó bien — `plugin build` la va a reintentar solo después).
  - `internal/plugins.UpdateAll` (`plugin update --all`) ahora suma
    una fila más al resultado por la copia compartida, si ya está
    clonada — mismo formato que cualquier otro repo.
  - Verificado en vivo, con los repos públicos reales (`HOME`
    aislado para no tocar los plugins ya instalados en esta máquina):
    `plugin install` de `asterion-firewall-analysis` deja
    `asterion-plugin-contract` clonada al lado sola; `plugin build`
    sobre ese mismo plugin ahora compila de punta a punta (binario Go
    + su frontend); una segunda corrida es idempotente (no re-clona
    nada); `plugin update --all` muestra `asterion-plugin-contract`
    como fila propia junto al plugin.

### Fixed
- **`make install`/`make build` fallaban en una instancia recién clonada
  — ni siquiera compilaban `asterion`.** Reporte en vivo del usuario,
  probando exactamente el escenario para el que se pensó `asterion
  install prerequirements` (una instancia con SOLO `asterion-core`
  clonado): `make install` tiraba `replacement directory ../asterion-lab
  does not exist` (y lo mismo para `asterion-language`,
  `asterion-plugin-contract`) y cortaba ahí, sin instalar nada. Causa
  real: los `replace ../X` de `go.mod` hacen que Go necesite esas
  carpetas hermanas para resolver el grafo de módulos de **todo**
  `cmd/asterion` — no de comandos puntuales — así que ni siquiera se
  puede compilar el propio `asterion install prerequirements` sin que las
  carpetas que ese comando clona ya existan de antes. Problema
  preexistente (ninguno de los archivos que lo disparan es nuevo de esta
  sesión: `plugins.go`, `language.go`, `container.go`,
  `internal/plugins/install.go`), pero que tumbaba en la práctica el
  único punto de entrada pensado para resolverlo. De paso quedó
  desmentida una afirmación vieja de la documentación ("sin los repos
  hermanos, `asterion` sigue compilando y funcionando igual, solo
  quedan sin esos módulos los comandos que los usan") — Go no compila
  parcialmente así, el build entero fallaba.
  - `Makefile`: catálogo de los 4 repos repetido en shell puro (mismo que
    `internal/prereqs/prereqs.go`, comentario cruzado entre los dos para
    no desincronizarlos) en un target nuevo `prerequirements`, del que
    ahora `build` e `install` dependen (`build: prerequirements`,
    `install: prerequirements`). Se ejecuta ANTES de compilar, no
    después — clona lo que falte, no toca lo que ya está (mismo chequeo
    `git rev-parse HEAD`, no un simple `.git`). Verificado en vivo
    simulando una instancia recién clonada de verdad (solo
    `cmd/`+`internal/`+`go.mod`+`Makefile`, sin ningún hermano al lado):
    `make build` en frío clona los 4 y compila en un solo paso; correrlo
    de nuevo es casi instantáneo (nada que clonar) y no repite ningún
    clone.
  - `asterion install prerequirements` (el comando en sí) ahora también
    se recompila solo si clonó algo nuevo — pedido del usuario ("con los
    comandos ya listos para funcionar"): un binario ya compilado no
    cambia porque aparezca código nuevo en disco, así que sin esto el
    propio comando dejaba los repos clonados pero los comandos que
    dependían de ellos (`lab`, `plugin`, `language`, etc.) seguían sin
    funcionar hasta una recompilación manual aparte. Corre `go install
    ./cmd/asterion` internamente (sin `sudo` — a propósito no intenta
    tocar `/usr/local/bin`, que puede pedir contraseña de forma
    interactiva) y lo reporta en el resultado (texto y `--json`). Si
    `--dir` apunta a una carpeta sin `asterion-core`, se omite en
    silencio — no es un error, ese workspace no es el que compila este
    binario. Verificado en vivo: clonado + recompilado automático deja
    `lab`/`plugin`/`language` funcionando en el mismo binario ya
    instalado, sin paso manual aparte; una corrida posterior sin nada
    nuevo que clonar no dispara una recompilación de más (mtime del
    binario sin cambios).
  - `README.md` y `CliReference.tsx` (asterion-cloud) actualizados para
    reflejar el comportamiento real: `make install`/`make build` ya son
    autosuficientes en una máquina nueva, y qué hacer si se compila a
    mano con `go build`/`go install` sin pasar por `make` (esos SÍ
    siguen necesitando los hermanos ya presentes).

### Added
- **`asterion install prerequirements`, comando nuevo para clonar de una
  los repos hermanos que hacen falta.** Hasta ahora, armar el workspace de
  desarrollo (este repo más `asterion-lab`, `asterion-language`,
  `asterion-plugin-contract` — los tres referenciados con `replace ../X`
  en `go.mod` — y `asterion-shared`, dependencia editable de Python para
  `backend-core`) era 100% manual: tanto este README (`## Compilar`) como
  los docs públicos de asterion-cloud daban por sentado que esos 4 repos
  "ya estaban" clonados al lado, y solo `asterion upgrade` (que únicamente
  actualiza lo que ya existe en disco, `internal/upgrade`, sin lista fija
  a propósito) cubría el paso siguiente. Pedido del usuario: un comando
  que los clone al instante, sin fallar si se lo corre de nuevo.
  - Paquete nuevo `internal/prereqs`, deliberadamente separado de
    `internal/upgrade` (que declara explícito en su propio comentario que
    no tiene lista fija) — acá sí hace falta una, para saber de dónde
    clonar algo que todavía no existe. Catálogo fijo de 4 repos, todos
    públicos en GitHub. Quedan afuera a propósito los plugins oficiales
    (`asterion-mail-plugin-basic`, `asterion-firewall-analysis` — ya
    tienen su propio flujo completo vía `asterion plugin install <url>`)
    y el repo `asterion` en sí (solo documentación/versiones).
  - Clone shallow (`--depth 1`, mismo criterio que `internal/plugins.
    Install`) — rápido, y `git pull --ff-only` (lo que usa `upgrade`
    después) funciona igual sobre un clone shallow.
  - Idempotente de verdad, no solo "no falla": un repo ya clonado se
    detecta con `git rev-parse HEAD` (no un simple `os.Stat(.git)`) —
    si HEAD no resuelve (un clone que se cortó a mitad de camino, antes
    de que `git` termine de escribir las refs), se borra y se reclona en
    vez de quedar reportando "ya estaba" para siempre sobre un repo que
    en realidad nunca terminó de bajar.
  - Verificado en vivo contra GitHub real (los 4 repos son públicos): un
    workspace vacío con solo `asterion-core` clona los 4 hermanos en
    ~5.5s; correr el comando de nuevo sobre el mismo workspace da 4x "ya
    estaba" sin re-clonar nada; un clone interrumpido a propósito (`git
    clone` matado a mitad de la transferencia, dejando `.git/HEAD`
    apuntando a una rama que nunca se llegó a crear — el estado real que
    deja una interrupción, confirmado a mano) se detecta como no usable,
    se borra y se re-clona correctamente; y correrlo sobre el workspace
    real de desarrollo (con los 4 hermanos ya presentes) da 4x "ya
    estaba" sin tocar nada, confirmado con `git status --short` antes y
    después.
  - `README.md` (`## Compilar`) y los docs públicos de asterion-cloud
    (`CliReference.tsx`, `LabReference.tsx`) actualizados para mostrar
    este comando en vez de asumir el clonado manual.

### Changed
- **`README.md` puesto al día con todo lo de esta sesión** — el usuario
  pidió actualizar la documentación después de agregar `agent status`
  sin argumento, y al revisar aparecieron varios huecos más en el mismo
  README (no solo ese comando): `## Compilar` no mencionaba `make
  install` en absoluto (solo `go install` a secas, y en un lugar
  separado del que ya explicaba `make build`/`--version`); tampoco
  mencionaba `asterion upgrade`, justo donde ya se explica que hay que
  clonar los repos hermanos al lado. La sección de plugins no
  documentaba `plugin update`, `plugin connect --all` ni `plugin
  disconnect` en absoluto. Y de paso aparecieron referencias
  desactualizadas de antes del cambio de proyectos por id numérico a
  slug (`--project <id>`, `POST /projects/{id}/plugins/connect-local`)
  en dos lugares — corregidas a `<slug>`/`{project_slug}`, que es lo
  que la API realmente espera hoy.
- **`asterion agent status` ya no exige el nombre de la instancia —
  identifica sola la que representa esta máquina.** Reporte del usuario:
  tenía que acordarse y tipear el nombre exacto (`self-<hostname>`) cada
  vez. Ahora, sin argumento, busca en el inventario local la instancia
  marcada con `Host=localhost` — la misma marca que `cloud install-agent`
  ya deja siempre, independiente del nombre elegido (con o sin `--name`).
  Cero instancias así → error claro pidiendo correr `install-agent`
  primero; más de una (inusual, alguien agregó "localhost" a mano) →
  lista los nombres candidatos y pide uno explícito en vez de adivinar
  cuál. Pasar un nombre sigue funcionando exactamente igual que antes.
  Verificado en vivo (sin necesitar backend, es 100% local): sin
  instancias, con una, con dos, y con nombre explícito — los cuatro
  casos.
- **`cloud disconnect` y `plugin disconnect` ahora piden confirmación por
  código de un solo uso, mandado al email de la sesión activa** — a
  pedido explícito del usuario: "que nadie pueda apretar directamente
  desconectar". Reusa tal cual el mismo mecanismo de `cloud login`
  (`RequestLoginCode`/`VerifyLoginCode`, mismo SMTP de la plataforma) en
  vez de inventar uno nuevo — la sesión que devuelve `VerifyLoginCode` se
  descarta a propósito (ya hay una vigente), lo único que importa es que
  falle si el código está mal. El email va siempre a la dirección de la
  sesión guardada (`cliconfig.Credentials.Email`), nunca a una que se
  tipee en el momento — así una sesión de CLI abierta en una máquina
  compartida no alcanza sola para desconectar nada, hace falta además
  poder leer ese correo. Nuevos helpers compartidos en `cloud.go`
  (`confirmByEmailCode`, `requireSessionEmail`), usados por los dos
  comandos. Verificado extremo a extremo contra un servidor real (con
  Firebase mockeado — `create_custom_token`/`exchange_custom_token_for_session`
  pegan contra Firebase de verdad y no hacen falta para esto — y el
  código fijado a un valor conocido en vez de al azar, para poder
  probarlo sin leer un email real): código incorrecto → 400 desde el
  backend, el CLI aborta con "no se hizo nada", y confirmado en la base
  que la instancia/plugin sigue conectado; código vacío → aborta antes
  de llamar a nada; código correcto → desconecta de verdad, confirmado
  en la base.
- **`make install` ahora instala en DOS lugares: `$GOPATH/bin` (vía `go
  install`) Y `/usr/local/bin` (copiado, con `sudo` si hace falta).**
  Encontrado en vivo, en dos vueltas: primero, en una instancia con un
  `asterion` viejo suelto en `/usr/local/bin` (de antes de usar `go
  install`), `git pull` + `go install ./cmd/asterion` compilaban bien
  el binario nuevo en `$GOPATH/bin`, pero como `/usr/local/bin` está
  antes en el `$PATH`, seguía corriendo el viejo sin ningún error —
  silencioso y confuso (primer intento de arreglo: solo avisar de la
  discrepancia). Segunda vuelta, ya sacado el binario viejo de en
  medio: bash tenía la ruta vieja *cacheada* (`hash`), así que
  `-bash: /usr/local/bin/asterion: No such file or directory` incluso
  con `/usr/local/bin/asterion` ya borrado — más confuso todavía.
  Solución real: en vez de pelear con cuál gana en el `$PATH` o con el
  caché de la shell, mantener los DOS lugares siempre con el mismo
  binario — no importa cuál resuelva primero. `/usr/local/bin` ya está
  en el `$PATH` de cualquier shell sin configurar nada, así que
  cubre el caso común de entrada. Sigue avisando si aparece un
  *tercer* binario en el medio que no sea ninguno de los dos.
  **Bug real encontrado en la propia verificación de este cambio**: la
  primera versión hacía `cp`/`sudo cp` sin chequear si había
  funcionado — con `sudo` fallando por falta de terminal interactiva
  para la contraseña (exactamente lo que pasa corriendo esto sin una
  sesión interactiva de verdad), el script igual imprimía "✓ copiado a
  /usr/local/bin" — un falso éxito. Corregido: ahora se chequea el
  código de salida del `cp`/`sudo cp` y, si falló, se avisa
  explícitamente con el comando exacto para correr a mano en vez de
  mentir que ya quedó listo. Verificado: la rama de éxito contra un
  directorio de prueba escribible (sin poder probar el `sudo` real sin
  la contraseña del usuario, es la única diferencia con ese camino), y
  la rama de error confirmada en vivo — un `sudo` real fallando por
  falta de terminal ahora sí se reporta como lo que es.
- **`asterion cloud disconnect <nombre-local> --project <slug>`** y
  **`asterion plugin disconnect <name> [--project <slug>]`** — lo que
  hacía falta para poder reconectar una instancia o plugin a OTRO
  proyecto de Asterion Cloud: mientras siga conectado a uno, el backend
  ya rechazaba (409) cualquier intento de conectarlo a un proyecto
  distinto (protección real, confirmada en vivo), pero no había forma
  de soltarlo desde el CLI. `cloud disconnect` pide `--project` siempre
  (`localstore.Instance` no recuerda a qué proyecto quedó conectada);
  `plugin disconnect` lo pide solo si hace falta — por default usa el
  que `plugin connect` ya guardó en `state.json`. Ninguno de los dos
  toca nada local (perfil SSH / instalación del plugin) — solo el
  registro del lado de Cloud, vía dos métodos nuevos en
  `internal/apiclient` (`DeleteInstance`, `DisconnectPlugin`).
  Verificado extremo a extremo contra un servidor real con `$HOME`
  aislado (sin tocar sesión/config real): conectar a un proyecto,
  confirmar que conectar a otro se rechaza, desconectar, confirmar que
  ahora sí se puede conectar al otro — para instancia y para plugin.
  De paso encontré y arreglé, del lado de `asterion-cloud`, un bug real
  donde un plugin desconectado quedaba bloqueado para siempre en vez de
  quedar libre para reconectarse (ver su CHANGELOG).
- **`asterion plugin connect --all`** — conecta todos los plugins
  instalados en esta máquina al mismo proyecto de Asterion Cloud (el
  proyecto se resuelve una sola vez, interactivo o vía `--project`, y se
  reusa para todos) en vez de correr `connect` una vez por plugin. Mismo
  criterio que `plugin update --all`: un plugin que falla no corta el
  resto, se reporta y se sigue. La lógica de conectar UN plugin se separó
  a `connectOne`/`connectResult` (antes vivía inline en el `RunE` del
  comando) para que el camino de a uno y el de `--all` compartan
  exactamente el mismo código, sin dos implementaciones que puedan
  desincronizarse. Verificado: `--help`, el rechazo de pasar nombre Y
  `--all` juntos, y que sin sesión guardada falla igual de claro que el
  camino de un solo plugin (mismo punto de falla, antes de cualquier
  llamada real a la API) — la conexión real contra Cloud no se probó en
  vivo en esta verificación porque requiere una sesión logueada de
  verdad y crea un registro real en producción.
- **`asterion --version`/`-v`** — imprime `asterion version vX.Y`. Un `go
  build` plano queda en `dev` (no hay forma de saber la versión real sin
  un paso explícito); `make build` (Makefile nuevo) compila el mismo
  binario pero le graba la versión sacándola del prefijo `VX.Y` del
  mensaje del último commit — la convención informal de versionado que ya
  usan los commits de este repo y del resto del ecosistema, mientras no
  haya releases etiquetados en git de verdad. `-X main.version=...` vía
  `-ldflags`, mismo mecanismo que usa cualquier CLI en Go (kubectl,
  docker, terraform) para esto — nada leyendo git en tiempo de ejecución,
  así que funciona igual para un binario compilado en otra máquina sin
  el repo clonado. `make version` imprime solo el número, para scripts.
  El flag en sí es el mecanismo nativo de cobra (`rootCmd.Version = ...`,
  agrega `--version` y, si nada más lo usa, `-v` solo) — confirmado sin
  colisión con ningún otro flag del CLI.
- **`asterion plugin update [name] [--all]`** — cierra el gap que el
  propio spec de APC dejó anotado a propósito ("no hay un paso de
  'update' separado en v1... hasta que exista una necesidad real"): `git
  pull --ff-only` sobre el repo clonado de un plugin instalado
  (`~/.config/asterion/plugins/repos/<name>`), o de todos con `--all`. Los
  plugins `--link` se saltan siempre — ni siquiera necesitan ser un repo
  git (ver `plugin install --help`), un pull automático ahí pisaría el
  propio trabajo en desarrollo del autor. Un repo con `--all` que falla
  (cambios propios sin commitear, rama divergida) no corta el resto. Si
  el pull trajo `plugin.yaml` nuevo, `state.json` se refresca con el
  manifest actualizado en el momento — mismo criterio que ya usa `Start()`
  al arrancar, aplicado ahora también acá. Actualizar el código no
  recompila ni reinicia el plugin solo (`asterion plugin build`/`stop`+
  `start` siguen siendo pasos aparte).
- **`asterion upgrade [name] [--dir] [--list]`** — package nuevo,
  `internal/upgrade`, sin relación con lo anterior: actualiza los repos
  hermanos del propio ecosistema Asterion en el workspace de desarrollo
  (`asterion-core`, `asterion-language`, `asterion-plugin-contract`,
  etc.), nunca la carpeta de plugins instalados. Encuentra el workspace
  subiendo desde el directorio actual hasta hallar una carpeta con
  `asterion-core` adentro (sin ruta fija hardcodeada), y lista cualquier
  subcarpeta `asterion*` con `.git` propio — `asterion-plugin-contract`
  es un repo más de esa lista, una sola vez en el filesystem aunque
  `asterion-core` y `asterion-language` lo referencien cada uno con su
  propio `replace` en `go.mod`.
  - **Bug real encontrado en la verificación de este cambio**: un `git
    pull` sin argumentos falla con "There is no tracking information for
    the current branch" en cualquier repo cuya rama actual no tenga
    upstream configurado (pasa hoy con `asterion-language`, clonado/creado
    sin `git push -u`) — no es un problema del repo, es que git no
    adivina solo. Se agregó un fallback: si el pull simple falla por eso
    específicamente, se reintenta una vez, explícito, contra
    `origin/<rama actual>` — mismo resultado que un `git pull origin
    main` a mano. Mismo fix aplicado a `plugin update` (comparten la
    lógica de pull).
  - Verificado en vivo contra los 9 repos reales del workspace: `--list`
    los encuentra todos correctamente (incluido `asterion-plugin-contract`
    una sola vez); actualizar de a uno y todos juntos funciona (`git
    pull --ff-only`, con el fallback de tracking cubriendo
    `asterion-language`); nombre inexistente y `--dir` apuntando a una
    carpeta sin `asterion-core` dan error claro; `--json` da una forma
    estable.

### Changed
- **`asterion plugin from-ast` → `asterion plugin from-asterion`**,
  siguiendo el rename de extensión en el repo hermano `asterion-language`
  (`.ast` → `.asterion` — colisionaba con el significado de "Abstract
  Syntax Tree"). `cmd/asterion/plugin_from_ast.go` renombrado a
  `plugin_from_asterion.go`; `asterion language check` también actualiza
  su string de uso a `<archivo.asterion>`. Verificado: `go build`/`go vet`
  sin errores, y `plugin from-asterion` recompilando el manifiesto real
  de `asterion-mail-plugin-basic` reproduce el `plugin.yaml` ya
  commiteado sin cambios.
- **`--project` pasa de ser un número a ser el slug del proyecto** (ej.
  `mi-proyecto-a3f9c1`), siguiendo el mismo cambio del lado de Asterion
  Cloud (ver su CHANGELOG: los proyectos dejaron de direccionarse por id
  numérico secuencial, enumerable, a favor de un slug). Afecta a los
  comandos que hablan con Cloud: `cloud connect`, `cloud install-agent`,
  `cloud uninstall-agent`, `plugin connect`, `instances list --project`,
  `cloud-accounts list/create --project`, `provision list/describe
  --project`.
  - `internal/apiclient/client.go`: los 8 métodos que llamaban
    `/projects/{id}/...` (`ConnectLocalInstance`, `ConnectLocalPlugin`,
    `ListPlugins`, `ListCloudAccounts`, `CreateCloudAccount`,
    `ListInstances`, `ListProvisioningRequests`,
    `CreateProvisioningRequest`) pasan de `projectID int` a
    `projectSlug string`.
  - `resolveProjectID` (`cmd/asterion/cloud.go`) pasa a llamarse
    `resolveProjectSlug` — el picker interactivo ahora muestra el slug
    de cada proyecto en vez de su id, y "tipear el ID directo" pasa a
    "tipear el slug directo". El orden del picker (más viejo primero)
    se preservó usando `created_at` en vez del id numérico ascendente,
    que ya no forma parte de lo que el usuario ve.
  - `internal/plugins/store.go`: el campo persistido
    `ConnectedProjectID int` (`connected_project_id` en
    `state.json`) pasa a `ConnectedProjectSlug string`
    (`connected_project_slug`). Efecto secundario aceptado a propósito:
    un plugin que ya estaba conectado antes de este cambio va a
    aparecer como no conectado hasta correr `asterion plugin connect`
    de nuevo — no hay releases con usuarios externos todavía como para
    justificar escribir una migración de este archivo de estado local.
  - Verificado con el binario real compilado contra un servidor HTTP
    mock (mismo criterio que ya se usó para probar `plugin connect`
    encadenado con `resolveProjectID` — ver más abajo en este mismo
    archivo): `--project <slug>` explícito, elegir por número en el
    picker interactivo, y tipear el slug directo en el picker — los
    tres confirmados a nivel HTTP real (el servidor mock logueó el path
    exacto recibido en cada caso). `go build`/`go vet`/`go test ./...`
    limpios.

### Added
- **`asterion plugin find`**: versión legible-por-humano de `plugin list`
  (que siempre imprime JSON, para backend-core) — nombre, versión,
  descripción, si está corriendo, y a qué proyecto de Asterion Cloud está
  conectado cada plugin instalado en esta máquina. No distingue origen:
  un plugin propio instalado con `--link` aparece igual que uno de
  terceros clonado de un repo público.
- **`name`/`--project` opcionales en `asterion plugin connect`**: sin
  `name`, lista los plugins instalados para elegir uno (mismo mecanismo
  que `plugin find`); sin `--project`, reusa `resolveProjectID` de
  `cloud connect` (listar/crear proyecto). Mismo motivo que el de
  `cloud connect`: antes tirabas `plugin connect --project 1` sin el
  nombre y solo devolvía `accepts 1 arg(s), received 0`, sin ninguna
  ayuda para encontrar qué nombre local usar.
- **`--project` opcional en `asterion cloud connect`/`install-agent`**: si
  se omite, se listan los proyectos de la cuenta ya logueada
  (`GET /projects`) para elegir uno por número; si todavía no hay ninguno,
  el propio comando ofrece crear uno ahí mismo (`POST /projects`, nombre +
  descripción) en vez de cortar con "falta --project" y mandar al usuario
  a buscar el ID a mano en el dashboard. `--project <id>` sigue
  funcionando igual que antes para uso no interactivo/scripts. Nuevo
  método `apiclient.Client.CreateProject`. Verificado en vivo contra un
  servidor HTTP real (mock de `/projects` y `/instances/.../connect-local`
  con las mismas formas de respuesta que el backend real): flujo de
  alta de proyecto nuevo, selección por índice y por ID tipeado, y el
  caso de entrada inválida — los tres funcionando end-to-end con el
  binario compilado real, sin mockear código Go.
- **`asterion plugin build <nombre>`**: compila el binario de un plugin ya
  instalado (`go build`, hoy solo para `language.name: go`) y, si tiene
  frontend propio (`frontend/package.json`), también corre `pnpm install
  && pnpm build` — en un solo comando, en vez de acordarse la ruta exacta
  y los pasos a mano. Sigue siendo una acción explícita a propósito:
  `install`/`start` nunca la disparan solos — Asterion no ejecuta código
  de un plugin de terceros sin que el operador lo pida puntualmente, ni
  siquiera para compilarlo. De yapa, `plugin start` ahora reconoce el
  caso "el binario no existe todavía" y sugiere este comando en el
  mensaje de error, en vez de solo mostrar el `fork/exec ... no such
  file or directory` crudo.
- **`asterion local tunnel start/stop/status/config`**: expone un puerto
  local (por default, el de `local serve` si está corriendo) con una URL
  pública HTTPS real vía Cloudflare Tunnel — sin abrir puertos en el
  firewall ni depender de la IP pública. Sin nada configurado usa un
  quick tunnel gratis de Cloudflare (`*.trycloudflare.com`, sin cuenta ni
  dominio); `local tunnel config set --token ...` guarda (cifrado) el
  token de un túnel con nombre ya creado en el dashboard de Cloudflare,
  para exponerlo con dominio propio de ahí en adelante. Probado en vivo:
  URL real generada, `curl` externo respondido con `200` por Cloudflare.
  El cifrado (`internal/secretbox`) se extrajo de `internal/plugins`
  (antes privado ahí) para reusarlo acá sin duplicar la implementación —
  cada subsistema sigue con su propia clave, sin migración para
  instalaciones existentes de plugins.
- **`asterion core serve`**: el servicio de Provider Adapters
  (AWS/Azure/GCP/OCI) ahora también corre como subcomando del CLI, mismo
  puerto default (`:8090`) y misma lógica que el binario standalone
  `cmd/asterion-core` (que sigue existiendo aparte, para casos como una
  imagen de contenedor mínima con solo este servicio) — para no tener que
  compilar/distribuir un segundo binario solo para levantarlo en un
  deploy.
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
  `asterion plugin from-asterion` (compila un manifiesto declarado explícito en
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
- **Confirmado en vivo contra una máquina Linux real** (hasta ahora solo
  se había probado en macOS): Asterion Lab con backend QEMU/KVM (mismo
  YAML, mismo flujo `create`/`start`/`test`/`destroy`, misma regla `ufw`
  real por SSH), el backend Docker de Asterion Lab (laboratorio
  solo-contenedores y mixto contra un daemon Docker real), `internal/
  sysinfo` leyendo `/proc`/`/sys` reales, y `asterion cloud install-agent`
  instalando y corriendo el Remote Agent de verdad vía `systemd --user`.
  READMEs de este repo, `asterion-lab` y `asterion-firewall-analysis`
  actualizados para reflejar esto — las secciones "Qué falta" de cada uno
  ya no listan Linux como pendiente, solo Windows y lo que seguía sin
  implementar antes de esto.
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
- **Bug real encontrado al probar `plugin connect` encadenado con
  `resolveProjectID`**: cada prompt interactivo (`cloud connect`,
  `plugin connect`, etc.) creaba su propio `bufio.NewReader(os.Stdin)`.
  `bufio.Reader` hace lecturas anticipadas — cuando dos prompts se
  encadenan en el mismo comando (elegir un plugin y después un proyecto,
  en `plugin connect` sin argumentos), el primer reader ya le había
  adelantado bytes al segundo `\n` desde el fd real sin haberlos
  entregado todavía, así que el segundo prompt leía una línea vacía y
  fallaba con `respuesta inválida: ""` incluso con input correcto
  esperando en stdin. Reproducido en vivo con el binario real antes de
  arreglarlo. Fix: un único `*bufio.Reader` de stdin para todo el
  proceso (`cmd/asterion/util.go:stdin` + `readLine()`), en vez de uno
  por función — inmune a cualquier secuencia de prompts, presente o
  futura. Re-verificado en vivo después del fix: mismo flujo encadenado
  (elegir plugin #2, después proyecto #1) funcionando de punta a punta.
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
  `resources`/`actions`, típicamente vía `from-asterion --force`) mostraba en
  el dashboard y en `plugin list` datos permanentemente desactualizados
  hasta reinstalarlo. `Start()` ahora relee el manifest desde
  `installed.Dir` antes de arrancar el proceso (si la lectura falla, sigue
  con el último manifest válido conocido en vez de bloquear el arranque)
  — encontrado y corregido mientras se probaba en vivo la tabla de
  Endpoints del panel de plugin, contra `asterion-mail-plugin-basic`
  después de agregarle el resource `recipient-groups`.
