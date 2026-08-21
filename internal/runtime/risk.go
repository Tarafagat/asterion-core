package runtime

import (
	"fmt"
	"strings"
)

// RiskLevel es deliberadamente explícito sobre "no lo sé": un chequeo de
// seguridad que reporta LOW porque no pudo leer el estado real es peor que
// inútil, es engañoso. UNKNOWN existe para que eso nunca pase acá.
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
	RiskUnknown  RiskLevel = "unknown"
)

// SSHRiskAssessment responde la pregunta central del spec de seguridad de
// Asterion: "¿un cambio de firewall podría dejar al administrador sin
// acceso?" — combinando qué puerto usa SSH de verdad, si hay una sesión
// SSH activa ahora mismo, y si el firewall (cuando se puede leer) protege
// ese puerto explícitamente.
type SSHRiskAssessment struct {
	Level  RiskLevel `json:"level"`
	Reason string    `json:"reason"`
}

// AssessSSHRisk nunca inventa un ALLOW que no pudo confirmar. Si no puede
// leer las reglas del firewall (sin privilegios, el caso más común),
// devuelve UNKNOWN — no LOW — porque decir "está bien" sin haber podido
// mirar sería exactamente el escenario de bloqueo accidental que este
// chequeo existe para prevenir.
func AssessSSHRisk(ssh SSHInfo, fw FirewallInspection) SSHRiskAssessment {
	if !ssh.ServiceActive {
		return SSHRiskAssessment{Level: RiskLow, Reason: "El servicio SSH no está activo en esta máquina — no aplica un riesgo de bloqueo de acceso administrativo por SSH."}
	}

	portsLabel := "puerto desconocido"
	if len(ssh.Ports) > 0 {
		portsLabel = strings.Join(ssh.Ports, ", ")
	}

	if !fw.Readable {
		return SSHRiskAssessment{
			Level: RiskUnknown,
			Reason: fmt.Sprintf(
				"SSH está activo en %s, pero no se pudo leer el estado real del firewall (%s) para confirmar si ese puerto tiene una regla de permiso explícita. %s",
				portsLabel, fw.Backend, fw.Detail,
			),
		}
	}

	if fw.Backend == "ufw" {
		if strings.Contains(fw.RawRules, "Status: inactive") {
			level := RiskMedium
			if ssh.RunningOverSSH {
				level = RiskHigh
			}
			return SSHRiskAssessment{
				Level: level,
				Reason: fmt.Sprintf(
					"ufw está detectado pero INACTIVO — SSH en %s no está siendo filtrado hoy, así que no hay riesgo inmediato. El riesgo aparece si se habilita ufw con política 'deny' por defecto sin agregar antes una regla ALLOW explícita para %s — eso cortaría el acceso administrativo. Usá 'asterion firewall plan' antes de habilitarlo.",
					portsLabel, portsLabel,
				),
			}
		}
		if portAllowedInUFW(fw.RawRules, ssh.Ports) {
			level := RiskLow
			reason := fmt.Sprintf("ufw está activo y tiene una regla ALLOW para SSH en %s.", portsLabel)
			if ssh.RunningOverSSH {
				reason += " Esta misma sesión que ejecuta Asterion es una conexión SSH — la regla la protege."
			}
			return SSHRiskAssessment{Level: level, Reason: reason}
		}
		level := RiskCritical
		reason := fmt.Sprintf(
			"ufw está ACTIVO pero no se encontró una regla ALLOW explícita para SSH en %s. Si la política por defecto es 'deny incoming', el acceso administrativo por SSH puede estar cortado, o quedaría cortado en el próximo reinicio del servicio de red.",
			portsLabel,
		)
		if ssh.RunningOverSSH {
			reason += " Esta misma sesión corre sobre SSH — es la conexión que se perdería."
		}
		return SSHRiskAssessment{Level: level, Reason: reason}
	}

	// nftables/iptables: se pudo leer el ruleset crudo, pero Asterion
	// todavía no tiene un parser confiable de sus reglas para confirmar
	// automáticamente un ALLOW — declarar UNSUPPORTED acá en vez de
	// simular una lectura que no se hizo de verdad (ver capability system,
	// internal/safety). El texto crudo queda disponible para revisión manual.
	return SSHRiskAssessment{
		Level: RiskUnknown,
		Reason: fmt.Sprintf(
			"El firewall (%s) se pudo leer, pero Asterion todavía no interpreta automáticamente sus reglas para confirmar un ALLOW de SSH en %s — revisá 'raw_rules' a mano. Esta capacidad está declarada UNSUPPORTED, no simulada.",
			fw.Backend, portsLabel,
		),
	}
}

// portAllowedInUFW busca, línea por línea en la salida de `ufw status
// verbose`, una entrada ALLOW para alguno de los puertos SSH detectados.
// Es un match de texto sobre un formato que ufw mantiene estable a
// propósito para ser parseado — no una certeza absoluta (no distingue
// origen/interfaz), por eso el resultado sigue siendo parte de un
// "Reason" legible en vez de un booleano ciego.
func portAllowedInUFW(raw string, ports []string) bool {
	for _, line := range strings.Split(raw, "\n") {
		upper := strings.ToUpper(line)
		if !strings.Contains(upper, "ALLOW") {
			continue
		}
		for _, port := range ports {
			if strings.Contains(line, port+"/tcp") || strings.Contains(line, port+"/udp") || strings.HasPrefix(strings.TrimSpace(line), port+" ") {
				return true
			}
		}
	}
	return false
}
