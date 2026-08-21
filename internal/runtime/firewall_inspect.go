package runtime

import (
	"os/exec"
	"strings"
)

// FirewallInspection es el resultado de intentar leer el estado REAL del
// firewall — no solo "está instalado" (eso ya lo hace Discover() en
// discovery.go). ufw/nft/iptables exigen root para consultar reglas en
// Linux, así que en la mayoría de las corridas sin sudo esto va a
// devolver Readable=false — y eso tiene que quedar explícito, nunca
// interpretarse como "no hay reglas" (sería un falso negativo peligroso
// para un chequeo de seguridad).
type FirewallInspection struct {
	Backend  string `json:"backend"` // "ufw" | "nftables" | "iptables" | "none"
	Readable bool   `json:"readable"`
	Detail   string `json:"detail"` // por qué no es legible, o un resumen si sí lo es
	RawRules string `json:"raw_rules,omitempty"`
}

// InspectFirewall elige, entre los backends detectados por Discover(), cuál
// está probablemente administrando el tráfico: ufw es la capa de más alto
// nivel si está instalada (por debajo usa iptables/nftables igual, así que
// tiene sentido mirar primero ahí); si no, nftables; si no, iptables. Es
// una heurística documentada, no una certeza — Backend siempre refleja qué
// binario se usó para esta inspección.
func InspectFirewall(detected []string) FirewallInspection {
	has := func(name string) bool {
		for _, d := range detected {
			if d == name {
				return true
			}
		}
		return false
	}

	switch {
	case has("ufw"):
		return inspectUFW()
	case has("nftables"):
		return inspectNFTables()
	case has("iptables"):
		return inspectIPTables()
	default:
		return FirewallInspection{Backend: "none", Readable: false, Detail: "no se detectó ningún firewall administrable conocido"}
	}
}

func needsPrivileges(err error, stderr string) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "root") || strings.Contains(lower, "permission denied") || strings.Contains(lower, "operation not permitted")
}

func inspectUFW() FirewallInspection {
	out, err := exec.Command("ufw", "status", "verbose").CombinedOutput()
	text := string(out)
	if needsPrivileges(err, text) {
		return FirewallInspection{
			Backend: "ufw", Readable: false,
			Detail: "ufw necesita privilegios de root para consultar el estado real — corré 'asterion local doctor' con sudo para un chequeo de riesgo real, no asumido",
		}
	}
	if err != nil {
		return FirewallInspection{Backend: "ufw", Readable: false, Detail: "no se pudo consultar ufw: " + strings.TrimSpace(text)}
	}
	return FirewallInspection{Backend: "ufw", Readable: true, RawRules: strings.TrimSpace(text)}
}

func inspectNFTables() FirewallInspection {
	out, err := exec.Command("nft", "list", "ruleset").CombinedOutput()
	text := string(out)
	if needsPrivileges(err, text) {
		return FirewallInspection{
			Backend: "nftables", Readable: false,
			Detail: "nftables necesita privilegios de root para listar el ruleset — corré 'asterion local doctor' con sudo",
		}
	}
	if err != nil {
		return FirewallInspection{Backend: "nftables", Readable: false, Detail: "no se pudo consultar nftables: " + strings.TrimSpace(text)}
	}
	return FirewallInspection{Backend: "nftables", Readable: true, RawRules: strings.TrimSpace(text)}
}

func inspectIPTables() FirewallInspection {
	out, err := exec.Command("iptables", "-L", "-n").CombinedOutput()
	text := string(out)
	if needsPrivileges(err, text) {
		return FirewallInspection{
			Backend: "iptables", Readable: false,
			Detail: "iptables necesita privilegios de root para listar reglas — corré 'asterion local doctor' con sudo",
		}
	}
	if err != nil {
		return FirewallInspection{Backend: "iptables", Readable: false, Detail: "no se pudo consultar iptables: " + strings.TrimSpace(text)}
	}
	return FirewallInspection{Backend: "iptables", Readable: true, RawRules: strings.TrimSpace(text)}
}
