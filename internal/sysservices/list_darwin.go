//go:build darwin

package sysservices

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// unitNamePattern: forma real de un label de launchd — casi siempre
// reverse-DNS ("com.apple.fseventsd", "com.asterion.agent.inst_1"), pero
// launchd no exige ese formato, así que el patrón solo exige "no puede
// empezar con '-'" (mismo motivo que en Linux: que un nombre jamás se lea
// como un flag) y un charset conservador (alfanumérico/punto/guion/guion
// bajo). El límite de 255 es una cota de seguridad propia, no un límite
// documentado de launchd (a diferencia del de systemd, que sí lo es).
var unitNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,254}$`)

// protectedExact: sshd (login remoto — mismo self-sabotage obvio que
// protege el equivalente de Linux). El resto de la protección de macOS
// (el namespace "com.apple." completo) es por prefijo, no por lista
// exacta — ver applePrefixProtected/isDarwinProtected más abajo.
var protectedExact = map[string]bool{
	"com.openssh.sshd": true,
}

// agentUnitPrefix: el label real que usa instalación del propio agente en
// macOS — ver launchdLabel() en cmd/asterion/agent.go ("com.asterion.agent."
// + localID). Hoy ese LaunchAgent se instala siempre por usuario (~/Library/
// LaunchAgents, ver installAgentServiceDarwin), un dominio distinto del que
// este paquete inspecciona (dominio "system" = LaunchDaemons de root) — así
// que hoy ni siquiera aparece en List(). Se protege por prefijo igual, red
// de seguridad ante un futuro modo de instalación a nivel sistema, mismo
// criterio que el equivalente de Linux.
const agentUnitPrefix = "com.asterion.agent."

// applePrefixProtected: a diferencia de Linux (una denylist chica de
// nombres puntuales) acá se protege TODO el namespace "com.apple." por
// prefijo — Asterion no tiene ningún motivo legítimo para reiniciar/parar
// un daemon propio de Apple (la mayoría además están protegidos por SIP,
// pero no vale la pena depender de eso), y son la enorme mayoría de lo que
// aparece en el dominio "system" de cualquier Mac.
const applePrefixProtected = "com.apple."

// isDarwinProtected complementa a IsProtected() (que ya chequea
// protectedExact/agentUnitPrefix) con el prefijo ancho de "com.apple." —
// separado en su propia función porque IsProtected() en sysservices.go es
// código compartido entre las 3 plataformas y no sabe de este prefijo
// puntual de macOS.
func isDarwinProtected(name string) bool {
	return IsProtected(name) || strings.HasPrefix(name, applePrefixProtected)
}

// List parsea `launchctl print system` — el volcado de TODO el dominio
// "system" (los LaunchDaemons que corren como root, el equivalente
// conceptual de la unidad de systemd por defecto). launchctl no tiene
// ningún modo de salida estructurada (ni JSON ni nada parecido a
// `systemctl --output=json`) — a diferencia de Linux, acá no hay
// "preferir estructurado, caer a texto plano": el texto es la única
// fuente que existe, confirmado contra el propio man de launchctl.
//
// Limitación real de la plataforma, documentada en vez de disimulada:
// launchd solo sabe listar lo que está CARGADO ahora mismo — no existe
// (a diferencia de `systemctl list-units --all`) un comando que enumere
// "todo lo que se conoce, esté cargado o no".
func List() ([]Unit, error) {
	out, err := exec.Command("launchctl", "print", "system").Output()
	if err != nil {
		return nil, fmt.Errorf("no se pudo listar servicios de launchd: %w", err)
	}
	return annotateDarwin(parseSystemServices(out)), nil
}

// parseSystemServices interpreta el bloque "services = { ... }" del
// volcado de `launchctl print system` — cada línea de entrada trae
// exactamente 3 campos separados por espacio/tab (PID, un token de
// estado cuyo significado exacto Apple no documenta — "-", "(pe)", "(jt)",
// "=", o un entero pequeño — y el label), confirmado en vivo contra una
// corrida real. Se ignoran a propósito los bloques hermanos "attractive
// services"/"disabled services" que aparecen después del cierre del
// bloque principal — mismo criterio de alcance chico y honesto que el
// resto de este paquete, no un intento de cubrir cada rincón de launchd.
func parseSystemServices(out []byte) []Unit {
	var units []Unit
	inBlock := false
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case !inBlock && trimmed == "services = {":
			inBlock = true
			continue
		case inBlock && trimmed == "}":
			return units
		case !inBlock:
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		pid, status, label := fields[0], fields[1], fields[2]
		active := "inactive"
		if pid != "0" && pid != "-" {
			active = "active"
		}
		units = append(units, Unit{Name: label, LoadState: "loaded", ActiveState: active, SubState: status})
	}
	return units
}

func annotateDarwin(units []Unit) []Unit {
	for i := range units {
		units[i].Protected = isDarwinProtected(units[i].Name)
		units[i].Category = Classify(units[i].Name)
	}
	return units
}

// Get consulta UN label puntual — sin privilegios (leer estado nunca los
// necesita en launchd, igual que en Linux), mismo camino que usa
// Control() para "verificar" después de actuar (ver status() en
// control_darwin.go). Un label bien formado pero no cargado no es un
// error: se devuelve LoadState "not-found", mismo criterio que Linux.
func Get(unitName string) (Unit, error) {
	if !ValidUnitName(unitName) {
		return Unit{}, fmt.Errorf("nombre de servicio inválido: %q", unitName)
	}
	return status(unitName)
}
