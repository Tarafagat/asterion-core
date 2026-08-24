// Package runtime es el Runtime Engine: la capa que sabe cómo es LA
// MÁQUINA donde corre Asterion (no cómo crear una — eso es el Provisioning
// Engine / Provider Adapters) y cómo administrarla. La usan tanto
// `asterion local` (status/doctor/config) como `asterion agent-run` — un
// único lugar para "qué hay instalado acá", nunca duplicado entre los dos.
//
// Es deliberadamente de solo detección/lectura por ahora: sabe decir qué
// encontró (systemd, ufw, nginx, cloudflared, etc.) pero todavía no instala
// ni modifica nada de eso — aplicar cambios reales sobre firewall/proxy/
// tunnel de la máquina de alguien es demasiado sensible para no tener antes
// el flujo completo describir → planificar → confirmar → aplicar →
// verificar que el resto de Asterion ya exige, y ese flujo es la fase
// siguiente. Ver README.md § Estado actual.
package runtime

import (
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"asterion-core/internal/sysinfo"
)

// Environment es lo que el Runtime Engine pudo detectar sobre esta
// máquina — nunca inventa un valor: si no puede confirmarlo, reporta
// "unknown" o una lista vacía en vez de adivinar.
type Environment struct {
	System         sysinfo.Info `json:"system"`
	ServiceManager string       `json:"service_manager"` // systemd | launchd | unknown
	Privileges     string       `json:"privileges"`      // root | user
	Firewall       []string     `json:"firewall"`        // ufw, nftables, iptables, firewalld — puede haber más de uno instalado
	ReverseProxy   []string     `json:"reverse_proxy"`   // nginx, caddy, apache, traefik
	Tunnel         []string     `json:"tunnel"`          // cloudflared, tailscale
	TLS            string       `json:"tls"`             // unknown por ahora — ver comentario en detectTLS
	DiscoveredAt   time.Time    `json:"discovered_at"`
}

// candidateBinaries mapea cada componente detectable a los binarios que lo
// identifican en PATH — el método más simple y portable de detección
// (no asume una distro en particular ni necesita parsear `systemctl list-units`).
var (
	firewallBinaries     = map[string]string{"ufw": "ufw", "nftables": "nft", "iptables": "iptables", "firewalld": "firewall-cmd"}
	reverseProxyBinaries = map[string]string{"nginx": "nginx", "caddy": "caddy", "apache": "apache2", "traefik": "traefik"}
	tunnelBinaries       = map[string]string{"cloudflared": "cloudflared", "tailscale": "tailscale"}
)

func detectFromBinaries(candidates map[string]string) []string {
	found := []string{}
	for label, binary := range candidates {
		if _, err := exec.LookPath(binary); err == nil {
			found = append(found, label)
		}
	}
	return found
}

// socketfilterfwPath es el binario de la Application Firewall de macOS
// (System Settings → Network → Firewall) — no vive en el PATH normal, así
// que se chequea por ruta fija en vez de exec.LookPath.
const socketfilterfwPath = "/usr/libexec/ApplicationFirewall/socketfilterfw"

// detectFirewall: en Linux, qué herramientas de firewall administrable
// están instaladas (ufw/nftables/iptables/firewalld, vía detectFromBinaries
// como el resto). macOS no tiene un "instalado o no" — pf y la Application
// Firewall vienen siempre con el sistema operativo, así que acá se reporta
// cuáles de los DOS existen en esta instalación concreta (por si alguna
// vez falta alguno, ej. una imagen mínima) en vez de asumirlo. Cuál de los
// dos está REALMENTE activo es un chequeo aparte — ver InspectFirewall en
// firewall_inspect.go, igual que en Linux (Discover solo dice "existe",
// nunca "está prendido").
func detectFirewall() []string {
	if runtime.GOOS == "darwin" {
		found := []string{}
		if _, err := exec.LookPath("pfctl"); err == nil {
			found = append(found, "pf")
		}
		if _, err := os.Stat(socketfilterfwPath); err == nil {
			found = append(found, "application-firewall")
		}
		return found
	}
	return detectFromBinaries(firewallBinaries)
}

func detectServiceManager() string {
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			return "systemd"
		}
	}
	if runtime.GOOS == "darwin" {
		return "launchd"
	}
	return "unknown"
}

func detectPrivileges() string {
	// os.Geteuid() no existe en Windows — ahí no podemos distinguir root
	// de usuario normal con esta técnica, así que queda "unknown".
	if runtime.GOOS == "windows" {
		return "unknown"
	}
	if os.Geteuid() == 0 {
		return "root"
	}
	return "user"
}

// detectTLS queda deliberadamente "unknown": confirmar TLS de verdad
// implica parsear configuración de Nginx/Caddy o certificados de Let's
// Encrypt, que depende de cuál reverse proxy se detectó — se resuelve
// junto con los adapters de cada proxy en la fase siguiente, no acá.
func detectTLS() string {
	return "unknown"
}

// Discover corre el paso "Environment Discovery" del flujo de
// `asterion local status`/`doctor`: al inicio, bajo demanda — nunca en
// loop (spec §48, "Environment Discovery: al inicio + bajo demanda").
func Discover() (Environment, error) {
	info, err := sysinfo.GatherInfo()
	if err != nil {
		return Environment{}, err
	}
	return Environment{
		System:         info,
		ServiceManager: detectServiceManager(),
		Privileges:     detectPrivileges(),
		Firewall:       detectFirewall(),
		ReverseProxy:   detectFromBinaries(reverseProxyBinaries),
		Tunnel:         detectFromBinaries(tunnelBinaries),
		TLS:            detectTLS(),
		DiscoveredAt:   time.Now(),
	}, nil
}

// PortListening es un chequeo liviano y real (no una suposición): intenta
// conectarse de verdad al puerto para saber si algo lo está escuchando.
func PortListening(host string, port int) bool {
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
