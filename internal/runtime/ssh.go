package runtime

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SSHInfo es lo que se pudo confirmar sobre el servicio SSH de esta
// máquina. Asterion NUNCA asume que SSH usa el puerto 22 — Ports queda
// vacío con Source explicando por qué si no se pudo determinar, en vez de
// devolver [22] como adivinanza.
type SSHInfo struct {
	ServiceDetected bool         `json:"service_detected"` // systemd conoce un servicio ssh/sshd (activo o no)
	ServiceActive   bool         `json:"service_active"`
	Ports           []string     `json:"ports"`       // puerto(s) efectivos detectados, ej. ["22"] o ["2222", "22"]
	PortSource      string       `json:"port_source"` // de dónde salió Ports: "sshd_config" | "listening_sockets" | "default_no_directive" | "unknown"
	CurrentSessions []SSHSession `json:"current_sessions"`
	RunningOverSSH  bool         `json:"running_over_ssh"` // si el propio proceso de asterion corre dentro de una sesión SSH (SSH_CONNECTION/SSH_CLIENT)
}

type SSHSession struct {
	User string `json:"user"`
	TTY  string `json:"tty"`
	From string `json:"from"` // host/IP de origen si `who` lo reporta; vacío si es una sesión local
}

// DiscoverSSH nunca falla — si no puede confirmar algo, ese campo queda en
// su valor vacío. Es deliberadamente conservador: para un chequeo de
// seguridad, "no sé" tiene que ser distinguible de "no hay nada".
func DiscoverSSH() SSHInfo {
	info := SSHInfo{}
	info.ServiceDetected, info.ServiceActive = discoverSSHService()
	info.Ports, info.PortSource = discoverSSHPorts()
	info.CurrentSessions = discoverSSHSessions()
	info.RunningOverSSH = os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_CLIENT") != ""
	return info
}

func discoverSSHService() (detected, active bool) {
	if !commandAvailable("systemctl") {
		return false, false
	}
	for _, name := range []string{"ssh", "sshd"} {
		out, err := exec.Command("systemctl", "is-active", name).Output()
		state := strings.TrimSpace(string(out))
		if err == nil && state == "active" {
			return true, true
		}
		if state == "inactive" || state == "failed" {
			detected = true // el servicio existe como unit, aunque no esté corriendo
		}
	}
	return detected, false
}

// discoverSSHPorts intenta, en orden de confianza decreciente:
//  1. `sshd -T` (config efectiva real, incluye sshd_config.d/*) — el más
//     confiable, pero puede fallar sin privilegios según la distro.
//  2. Directivas "Port" en sshd_config y sshd_config.d/*.conf — no es la
//     config efectiva (podría haber un valor por defecto de compilación
//     distinto), pero es mejor que adivinar.
//  3. Si no hay ninguna directiva explícita y el servicio está activo: el
//     comportamiento default documentado de OpenSSH es el puerto 22 — se
//     reporta como tal, marcado explícitamente como default, no como algo
//     confirmado.
func discoverSSHPorts() ([]string, string) {
	if commandAvailable("sshd") {
		if out, err := exec.Command("sshd", "-T").Output(); err == nil {
			ports := parsePortDirectives(string(out))
			if len(ports) > 0 {
				return ports, "sshd_config" // -T ya resuelve config.d, includes, y defaults
			}
		}
	}

	ports := map[string]bool{}
	for _, path := range sshdConfigFiles() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, p := range parsePortDirectives(string(data)) {
			ports[p] = true
		}
	}
	if len(ports) > 0 {
		result := make([]string, 0, len(ports))
		for p := range ports {
			result = append(result, p)
		}
		return result, "sshd_config"
	}

	detected, _ := discoverSSHService()
	if detected {
		return []string{"22"}, "default_no_directive"
	}
	return nil, "unknown"
}

func sshdConfigFiles() []string {
	files := []string{"/etc/ssh/sshd_config"}
	entries, err := os.ReadDir("/etc/ssh/sshd_config.d")
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".conf") {
				files = append(files, filepath.Join("/etc/ssh/sshd_config.d", e.Name()))
			}
		}
	}
	return files
}

func parsePortDirectives(content string) []string {
	var ports []string
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.EqualFold(fields[0], "port") {
			if _, err := strconv.Atoi(fields[1]); err == nil {
				ports = append(ports, fields[1])
			}
		}
	}
	return ports
}

// discoverSSHSessions usa `who`, disponible sin privilegios en cualquier
// distro — muestra sesiones locales y remotas (con el host de origen entre
// paréntesis para las que llegaron por red, SSH incluido).
func discoverSSHSessions() []SSHSession {
	if !commandAvailable("who") {
		return nil
	}
	out, err := exec.Command("who").Output()
	if err != nil {
		return nil
	}
	var sessions []SSHSession
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		session := SSHSession{User: fields[0], TTY: fields[1]}
		if idx := strings.Index(scanner.Text(), "("); idx != -1 {
			from := scanner.Text()[idx:]
			from = strings.TrimPrefix(from, "(")
			from = strings.TrimSuffix(from, ")")
			session.From = from
		}
		sessions = append(sessions, session)
	}
	return sessions
}
