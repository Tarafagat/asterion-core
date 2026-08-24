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
  (transforma una API REST propia en un `plugin.yaml` de partida). Incluye
  un plugin de referencia funcionando (`dummy-fs-provider`) con tests
  reales.
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

### Changed
- Licencia: Apache License 2.0 (ver [LICENSE](LICENSE)) — explícita a
  propósito, para maximizar la adopción del ecosistema de plugins. Ver
  la sección "Licencia" del README para el porqué de la separación
  respecto a la licencia de Asterion Cloud (AGPLv3).
