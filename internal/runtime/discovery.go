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
		Firewall:       detectFromBinaries(firewallBinaries),
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
