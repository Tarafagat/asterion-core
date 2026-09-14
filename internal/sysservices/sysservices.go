// Package sysservices es el motor de lectura/control de unidades systemd
// tipo .service de ESTA máquina — todas las que systemd conoce, no solo las
// de Asterion. Una sola implementación, compartida entre
// `asterion local info services` (CLI, esta misma máquina) y el executor
// de jobs del agente (cmd/asterion/agentjobs.go, disparado desde Asterion
// Cloud) — nunca duplicada entre los dos.
//
// A diferencia de internal/runtime (que solo detecta PRESENCIA de systemd
// vía /run/systemd/system, deliberadamente sin parsear `list-units` — ver
// su package doc), acá sí hace falta parsear la salida real: la pregunta es
// otra ("¿qué unidades existen y en qué estado?", no "¿hay systemd?").
//
// List() es de solo lectura y no necesita privilegios (systemd permite
// listar/inspeccionar unidades a cualquier usuario local sin sudo).
// Control() sí muta y sí necesita sudo — ver la sudoers grant opt-in
// ('asterion agent enable-service-control') en cmd/asterion/agent.go.
//
// Este paquete NO se registra en internal/safety como un adapter con
// Apply+Rollback (comparar con OSUserAdapter): "reiniciar un proceso" no
// tiene un Rollback honesto (no existe "des-reiniciar" algo), y declarar
// esa capability sería simular una que no se implementó — exactamente lo
// que ese paquete existe para evitar. Ver internal/safety/adapters.go
// (SystemServiceAdapter) para el detalle de qué sí se declara ahí.
package sysservices

import (
	"regexp"
	"strings"
)

// Unit es una unidad .service tal como la reportó systemd — Protected se
// calcula acá, no en Cloud, para que exista un único lugar (este paquete)
// que decida qué está protegido, reusado por CLI, discovery y control.
type Unit struct {
	Name        string   `json:"name"`
	LoadState   string   `json:"load_state"`
	ActiveState string   `json:"active_state"`
	SubState    string   `json:"sub_state"`
	Description string   `json:"description"`
	Protected   bool     `json:"protected"`
	Category    Category `json:"category"`
}

// unitNamePattern: empieza con alfanumérico (nunca '-', para que un nombre
// de unidad jamás pueda leerse como un flag) y termina en ".service". El
// límite de largo (255 en total, incluido ".service") es el límite real de
// systemd para nombres de unidad. Debe coincidir carácter por carácter con
// UNIT_NAME_PATTERN en
// asterion-cloud/backend/app/schemas/system_services.py — no hay forma de
// compartir código entre Go y Python, así que se sincroniza a mano;
// cualquier cambio acá tiene que reflejarse allá también.
//
// Deliberadamente más angosto que el charset real de systemd (que además
// permite ':' y '\' para unidades escaped/template) — no hace falta para
// unidades .service comunes, y cada carácter permitido de más es
// superficie de ataque de más.
var unitNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._@-]{0,246}\.service$`)

// ValidUnitName confirma que el nombre tiene la forma de una unidad
// .service real — se llama tanto antes de ejecutar Control como dentro de
// ella (nunca confiar en que el caller ya validó).
func ValidUnitName(name string) bool { return unitNamePattern.MatchString(name) }

// ValidAction confirma que la acción es una de las 3 soportadas — ninguna
// otra palabra llega jamás a exec.Command.
func ValidAction(action string) bool {
	switch action {
	case "start", "stop", "restart":
		return true
	default:
		return false
	}
}

// protectedExact: unidades que el agente se niega a tocar pase lo que pida
// Cloud — red de seguridad mínima, NO un policy engine (a propósito corta).
// sshd + la pila de red son los self-sabotage obvios: perder acceso
// remoto, o perder conectividad, sin ninguna forma de recuperación que no
// sea consola/acceso físico.
var protectedExact = map[string]bool{
	"ssh.service":              true,
	"sshd.service":             true,
	"networking.service":       true,
	"systemd-networkd.service": true,
	"NetworkManager.service":   true,
}

// agentUnitPrefix protege la propia unidad supervisora del agente
// ("asterion-agent-<id>.service", ver installAgentService en
// cmd/asterion/agent.go). Hoy esa unidad se instala siempre vía
// `systemctl --user` (bus de sesión), un scope distinto del que este
// paquete inspecciona (`systemctl` a secas = bus de sistema) — así que hoy
// ni siquiera aparece en List(). Se protege por prefijo igual, como red de
// seguridad ante un futuro modo de instalación a nivel sistema, sin
// depender de que "hoy no aplica" siga siendo cierto para siempre. Prefijo
// (no nombre exacto con id) a propósito: no hace falta conocer el localID
// de esta instancia para protegerse a sí mismo.
const agentUnitPrefix = "asterion-agent-"

// IsProtected confirma si esta unidad está en la denylist — Control la
// rechaza siempre, sin importar qué pida Cloud.
func IsProtected(name string) bool {
	return protectedExact[name] || strings.HasPrefix(name, agentUnitPrefix)
}
