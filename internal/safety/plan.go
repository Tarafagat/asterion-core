package safety

import (
	"strings"

	"asterion-core/internal/runtime"
)

// FirewallPlan es el resultado de `asterion firewall plan` (spec §12):
// puro Discover + análisis, nunca modifica nada. RollbackAvailable queda
// fijo en false — no porque el firewall no se pueda revertir en general,
// sino porque este paquete todavía no implementa ningún Apply del que
// hacer rollback (ver UFWAdapter.Capabilities(): CapApply/CapRollback
// ausentes). Reportar "disponible" acá sería mentir sobre una capacidad
// que no existe.
type FirewallPlan struct {
	SSH               runtime.SSHInfo            `json:"ssh"`
	Firewall          runtime.FirewallInspection `json:"firewall"`
	Risk              runtime.SSHRiskAssessment  `json:"risk"`
	ReverseProxy      []string                   `json:"reverse_proxy"`
	Tunnel            []string                   `json:"tunnel"`
	ProtectedServices []string                   `json:"protected_services"`
	ProposedChange    []string                   `json:"proposed_change"`
	RollbackAvailable bool                       `json:"rollback_available"`
	ApplyAvailable    bool                       `json:"apply_available"`
}

// BuildFirewallPlan es de solo lectura de punta a punta: Discover, Inspect,
// Assess — cero llamadas que modifiquen el sistema. Es intencionalmente
// el único camino que Asterion tiene hoy para "razonar" sobre el firewall;
// no existe un BuildFirewallPlan que aplique, ni una variante --apply de
// este comando.
func BuildFirewallPlan() (FirewallPlan, error) {
	env, err := runtime.Discover()
	if err != nil {
		return FirewallPlan{}, err
	}
	ssh := runtime.DiscoverSSH()
	fw := runtime.InspectFirewall(env.Firewall)
	risk := AssessSSHFirewallRisk(ssh, fw)

	protected := []string{}
	if ssh.ServiceActive {
		protected = append(protected, "SSH")
	}
	if len(env.ReverseProxy) > 0 {
		protected = append(protected, env.ReverseProxy...)
	}

	proposed := []string{"default incoming: deny", "default outgoing: allow"}
	for _, port := range ssh.Ports {
		proposed = append(proposed, "allow "+port+"/tcp   # SSH — "+ssh.PortSource)
	}
	if len(env.ReverseProxy) > 0 {
		proxies := strings.Join(env.ReverseProxy, ", ")
		proposed = append(proposed, "allow 80/tcp    # HTTP, reverse proxy detectado: "+proxies)
		proposed = append(proposed, "allow 443/tcp   # HTTPS, reverse proxy detectado: "+proxies)
	}

	return FirewallPlan{
		SSH:               ssh,
		Firewall:          fw,
		Risk:              risk,
		ReverseProxy:      env.ReverseProxy,
		Tunnel:            env.Tunnel,
		ProtectedServices: protected,
		ProposedChange:    proposed,
		RollbackAvailable: Has(UFWAdapter{}, CapRollback),
		ApplyAvailable:    Has(UFWAdapter{}, CapApply),
	}, nil
}
