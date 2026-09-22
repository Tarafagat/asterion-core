//go:build windows

package sysservices

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// unitNamePattern: forma real del "Name" (ServiceName) de un servicio de
// Windows — NO el DisplayName ("DNS Client", con espacios), que nunca es
// lo que este paquete recibe ni lo que usan Get-Service/sc.exe para
// identificar un servicio. El ServiceName real casi nunca tiene espacios
// (ej. "Dnscache", "wuauserv", "MySQL80", "postgresql-x64-14") — el
// patrón exige empezar alfanumérico (mismo motivo que en Linux/macOS: que
// un nombre jamás se lea como un flag) y un charset conservador. 255 es
// una cota de seguridad propia, no un límite documentado del Service
// Control Manager.
var unitNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]{0,254}$`)

// protectedExact: el equivalente de sshd (OpenSSH Server para Windows se
// registra como el servicio "sshd"), Remote Desktop ("TermService" — en
// Windows es el camino de acceso remoto más común, más crítico de
// proteger acá que SSH), WinRM (PowerShell remoto) y el resto de la red
// core (DNS/DHCP/compartición de archivos) — mismo criterio de "red de
// seguridad mínima, no un policy engine" que la lista de Linux.
var protectedExact = map[string]bool{
	"sshd":              true,
	"TermService":       true,
	"WinRM":             true,
	"Dnscache":          true,
	"Dhcp":              true,
	"LanmanServer":      true,
	"LanmanWorkstation": true,
}

// agentUnitPrefix: mismo prefijo que ya usa windowsTaskName() en
// cmd/asterion/agent.go ("asterion-agent-" + localID) para el Scheduled
// Task del propio agente — hoy el agente en Windows se instala como
// Scheduled Task, no como servicio SCM (ver installAgentServiceWindows),
// así que hoy ni siquiera aparece en List(). Se protege por prefijo igual,
// como red de seguridad ante un futuro modo de instalación como servicio
// real, con el mismo prefijo que ya existe en vez de inventar uno nuevo.
const agentUnitPrefix = "asterion-agent-"

// serviceInfo es la forma mínima que se le pide a PowerShell — Status se
// fuerza a string con .ToString() en el propio comando (ver List()/Get())
// porque ConvertTo-Json puede serializar un enum de .NET como su valor
// numérico en vez de su nombre según la versión de PowerShell, y no hay
// forma de probar esto en vivo desde este entorno de desarrollo (sin
// máquina Windows disponible) — se resuelve la ambigüedad de raíz pidiendo
// el string explícitamente, en vez de asumir un formato de salida.
type serviceInfo struct {
	Name        string `json:"Name"`
	DisplayName string `json:"DisplayName"`
	Status      string `json:"Status"`
}

// List prefiere PowerShell `Get-Service | ConvertTo-Json` (estructurado)
// y cae a `sc.exe query type= service state= all` (texto, formato estable
// documentado por Microsoft desde Windows 2000) si PowerShell no está
// disponible o devuelve algo inesperado — mismo criterio "preferir
// estructurado, caer a texto plano" que ya usa systemd en Linux.
//
// El pipeline envuelve el resultado en @(...) antes de ConvertTo-Json a
// propósito: sin eso, PowerShell serializa una lista de UN solo elemento
// como objeto JSON suelto en vez de array de un elemento — un gotcha
// conocido de ConvertTo-Json, no algo específico de este comando.
func List() ([]Unit, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`@(Get-Service | Select-Object Name, DisplayName, @{N='Status';E={$_.Status.ToString()}}) | ConvertTo-Json -Compress`,
	).Output()
	if err == nil {
		if units, parseErr := parsePowerShellJSON(out); parseErr == nil {
			return annotateWindows(units), nil
		}
		// El comando corrió pero la salida no fue el JSON esperado: cae al
		// parser de sc.exe en vez de fallar de punta a punta.
	}
	out, err = exec.Command("sc", "query", "type=", "service", "state=", "all").Output()
	if err != nil {
		return nil, fmt.Errorf("no se pudo listar servicios de Windows: %w", err)
	}
	return annotateWindows(parseScQuery(out)), nil
}

func parsePowerShellJSON(out []byte) ([]Unit, error) {
	var raw []serviceInfo
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	units := make([]Unit, 0, len(raw))
	for _, r := range raw {
		units = append(units, Unit{
			Name: r.Name, LoadState: "loaded",
			ActiveState: activeStateFromStatusWord(r.Status),
			SubState:    r.Status, Description: r.DisplayName,
		})
	}
	return units, nil
}

// activeStateFromStatusWord mapea el Status de Get-Service (o el nombre
// de STATE de sc.exe) al vocabulario de ActiveState que ya usa systemd —
// "Running" es el único valor que de verdad significa "activo"; el resto
// (Stopped/Paused/*Pending) cae a "inactive" en el resumen grueso, el
// valor real queda de todas formas en SubState sin perderse.
func activeStateFromStatusWord(status string) string {
	if strings.EqualFold(status, "Running") {
		return "active"
	}
	return "inactive"
}

// parseScQuery interpreta `sc query type= service state= all` — formato
// de texto estable, documentado por Microsoft, sin cambios entre
// versiones de Windows: bloques separados por línea en blanco, cada uno
// empieza con "SERVICE_NAME: <nombre>", trae "DISPLAY_NAME: <nombre>" y
// una línea "STATE : <número>  <PALABRA>" (ej. "4  RUNNING").
func parseScQuery(out []byte) []Unit {
	var units []Unit
	var cur *Unit
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "SERVICE_NAME:"):
			if cur != nil {
				units = append(units, *cur)
			}
			cur = &Unit{Name: strings.TrimSpace(strings.TrimPrefix(line, "SERVICE_NAME:")), LoadState: "loaded"}
		case cur == nil:
			continue
		case strings.HasPrefix(line, "DISPLAY_NAME:"):
			cur.Description = strings.TrimSpace(strings.TrimPrefix(line, "DISPLAY_NAME:"))
		case strings.HasPrefix(line, "STATE"):
			fields := strings.Fields(line)
			// fields: ["STATE", ":", "4", "RUNNING", ...] — la palabra de
			// estado es el token inmediatamente después del número.
			for i, f := range fields {
				if f == ":" || isAllDigits(f) {
					continue
				}
				if i > 0 && isAllDigits(fields[i-1]) {
					cur.SubState = f
					cur.ActiveState = activeStateFromStatusWord(f)
				}
				break
			}
		}
	}
	if cur != nil {
		units = append(units, *cur)
	}
	return units
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func annotateWindows(units []Unit) []Unit {
	for i := range units {
		units[i].Protected = IsProtected(units[i].Name)
		units[i].Category = Classify(units[i].Name)
	}
	return units
}

// Get consulta UN servicio puntual — sin privilegios (leer estado nunca
// los necesita), mismo camino que usa Control() para "verificar" después
// de actuar (ver status() en control_windows.go). Un nombre bien formado
// pero inexistente no es un error: se devuelve LoadState "not-found",
// mismo criterio que Linux/macOS.
func Get(unitName string) (Unit, error) {
	if !ValidUnitName(unitName) {
		return Unit{}, fmt.Errorf("nombre de servicio inválido: %q", unitName)
	}
	return status(unitName)
}
