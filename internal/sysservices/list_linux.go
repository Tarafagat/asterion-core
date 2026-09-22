//go:build linux

package sysservices

import (
	"fmt"
	"os/exec"
	"regexp"
)

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

// List corre `systemctl list-units --all --type=service`, prefiriendo
// --output=json (systemd moderno, ~v247+) y cayendo a texto plano si el
// flag no existe (systemd viejo — todavía común en producción: Ubuntu
// 18.04/20.04, Debian 10/11). Nunca requiere sudo: listar/inspeccionar
// unidades es una operación sin privilegios.
func List() ([]Unit, error) {
	if out, err := exec.Command("systemctl", "list-units", "--all", "--type=service", "--output=json", "--no-pager").Output(); err == nil {
		if units, parseErr := parseJSON(out); parseErr == nil {
			return annotate(units), nil
		}
		// --output=json existe pero devolvió algo inesperado: cae al
		// parser de texto en vez de fallar de punta a punta.
	}
	out, err := exec.Command("systemctl", "list-units", "--all", "--type=service", "--no-legend", "--no-pager", "--plain", "--full").Output()
	if err != nil {
		return nil, fmt.Errorf("no se pudo listar unidades systemd: %w", err)
	}
	return annotate(parsePlain(out)), nil
}

func annotate(units []Unit) []Unit {
	for i := range units {
		units[i].Protected = IsProtected(units[i].Name)
		units[i].Category = Classify(units[i].Name)
	}
	return units
}

// Get consulta UNA unidad puntual por nombre — sin sudo (leer estado nunca
// lo necesita), mismo camino que usa Control() para "verificar" después de
// actuar (ver status() en control.go). Sirve para nombrar una unidad
// directo (`asterion services list <nombre>`) sin tener que listar
// todas para filtrar una sola. Un nombre bien formado pero inexistente no
// es un error: systemctl devuelve LoadState "not-found" igual que
// cualquier otra consulta (mismo criterio que parsePlain ya contempla).
func Get(unitName string) (Unit, error) {
	if !ValidUnitName(unitName) {
		return Unit{}, fmt.Errorf("nombre de unidad inválido: %q", unitName)
	}
	return status(unitName)
}
