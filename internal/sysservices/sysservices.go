// Package sysservices es el motor de lectura/control de los servicios de
// ESTA máquina — systemd en Linux, launchd en macOS, el Service Control
// Manager en Windows — todos los que el gestor de servicios del sistema
// operativo conoce, no solo los de Asterion. Una sola API pública
// (List/Get/Control/Classify/IsProtected/ValidUnitName/ValidAction),
// compartida entre `asterion services list` (CLI, esta misma máquina) y el
// executor de jobs del agente (cmd/asterion/agentjobs.go, disparado desde
// Asterion Cloud) — nunca duplicada entre los dos, y nunca duplicada entre
// plataformas: cada SO implementa la misma firma en su propio archivo con
// build tag (list_linux.go/control_linux.go, list_darwin.go/
// control_darwin.go, list_windows.go/control_windows.go), el compilador
// elige el correcto según target.
//
// Los 3 gestores de servicios NO son variantes de una misma API — a
// diferencia de otros paquetes de este repo que solo separan Windows del
// resto (ver internal/localserve, internal/tunnel, internal/sysinfo, todos
// con un solo split "_unix.go"/"_windows.go" porque ahí Linux y macOS
// comparten la misma syscall POSIX), acá no hay código real que compartir
// entre systemd y launchd, así que el split es de 3 vías, no de 2.
//
// A diferencia de internal/runtime (que solo detecta PRESENCIA de systemd
// vía /run/systemd/system, deliberadamente sin parsear `list-units` — ver
// su package doc), acá sí hace falta parsear la salida real: la pregunta es
// otra ("¿qué unidades existen y en qué estado?", no "¿hay systemd?").
//
// List() es de solo lectura y no necesita privilegios (los 3 gestores
// permiten listar/inspeccionar servicios a cualquier usuario local sin
// privilegios elevados). Control() sí muta y sí necesita privilegios — en
// Linux vía sudo (ver la sudoers grant opt-in 'asterion agent
// enable-service-control' en cmd/asterion/agent.go); en macOS/Windows
// depende de que el proceso que llama ya corra elevado (root/SYSTEM), sin
// intentar ningún prompt interactivo (mismo criterio que `sudo -n`: falla
// rápido con un error claro en vez de colgarse).
//
// Nota de un gap conocido, no resuelto acá: UNIT_NAME_PATTERN y
// CONTROL_ACTION_PATTERN en
// asterion-cloud/backend/app/schemas/system_services.py asumen la forma de
// una unidad systemd (sufijo ".service") para validar las acciones de
// control remoto que llegan desde Cloud — un nombre de servicio de
// launchd o de Windows las rechazaría. Cloud/backend está fuera de
// alcance de este paquete; queda como trabajo futuro si algún día se
// habilita control remoto real desde el dashboard para agentes en Mac o
// Windows.
//
// Este paquete NO se registra en internal/safety como un adapter con
// Apply+Rollback (comparar con OSUserAdapter): "reiniciar un proceso" no
// tiene un Rollback honesto (no existe "des-reiniciar" algo), y declarar
// esa capability sería simular una que no se implementó — exactamente lo
// que ese paquete existe para evitar. Ver internal/safety/adapters.go
// (SystemServiceAdapter) para el detalle de qué sí se declara ahí.
package sysservices

import "strings"

// Unit es un servicio del sistema operativo tal como lo reportó su
// gestor (systemd/launchd/SCM según la plataforma) — Protected se calcula
// acá, no en Cloud, para que exista un único lugar (este paquete) que
// decida qué está protegido, reusado por CLI, discovery y control. Los
// nombres de los campos usan el vocabulario de systemd (LoadState/
// ActiveState/SubState) porque fue la primera plataforma implementada; los
// backends de macOS/Windows mapean su propio modelo de estado al más
// parecido de estos tres — ver el comentario de cada List()/Get() por
// plataforma para el detalle de ese mapeo.
type Unit struct {
	Name        string   `json:"name"`
	LoadState   string   `json:"load_state"`
	ActiveState string   `json:"active_state"`
	SubState    string   `json:"sub_state"`
	Description string   `json:"description"`
	Protected   bool     `json:"protected"`
	Category    Category `json:"category"`
}

// ValidUnitName confirma que el nombre tiene la forma de un
// nombre de unidad/servicio válido PARA LA PLATAFORMA ACTUAL (el patrón
// exacto — unitNamePattern — lo define cada archivo por SO) — se llama
// tanto antes de ejecutar Control como dentro de ella (nunca confiar en
// que el caller ya validó).
func ValidUnitName(name string) bool { return unitNamePattern.MatchString(name) }

// ValidAction confirma que la acción es una de las 3 soportadas — ninguna
// otra palabra llega jamás a exec.Command. Las 3 acciones son el mismo
// concepto lógico en los 3 sistemas operativos (aunque el comando real
// detrás de cada una difiera por plataforma — ver Control() en cada
// archivo por SO).
func ValidAction(action string) bool {
	switch action {
	case "start", "stop", "restart":
		return true
	default:
		return false
	}
}

// IsProtected confirma si este servicio está en la denylist de la
// plataforma actual (protectedExact/agentUnitPrefix, definidos por cada
// archivo por SO) — Control la rechaza siempre, sin importar qué pida
// Cloud.
func IsProtected(name string) bool {
	return protectedExact[name] || strings.HasPrefix(name, agentUnitPrefix)
}
