package safety

import "asterion-core/internal/runtime"

// inspectOnly son las capabilities de todo adapter de esta fase: puede
// detectar y leer el estado real, y armar un plan de solo lectura — nunca
// aplicar. Apply/Verify/Rollback quedan ausentes del mapa (UNSUPPORTED),
// no en false explícito, para que quede claro en el JSON que ni siquiera
// se intentó implementarlas todavía.
var inspectOnly = map[Capability]bool{
	CapDetect:  true,
	CapInspect: true,
	CapPlan:    true,
}

// UFWAdapter envuelve la inspección de firewall del Runtime Engine
// (internal/runtime) bajo el contrato Adapter — mismo dato, declarado con
// sus capabilities reales.
type UFWAdapter struct{}

func (UFWAdapter) Name() string                      { return "ufw" }
func (UFWAdapter) Capabilities() map[Capability]bool { return inspectOnly }

// SSHAdapter: descubre el servicio SSH real de la máquina (puerto
// efectivo, sesiones activas) — ver internal/runtime/ssh.go.
type SSHAdapter struct{}

func (SSHAdapter) Name() string                      { return "ssh" }
func (SSHAdapter) Capabilities() map[Capability]bool { return inspectOnly }

// ReverseProxyAdapter: detecta nginx/Caddy/Apache (binario + servicio) —
// ver internal/runtime/discovery.go.
type ReverseProxyAdapter struct{}

func (ReverseProxyAdapter) Name() string                      { return "reverse-proxy" }
func (ReverseProxyAdapter) Capabilities() map[Capability]bool { return inspectOnly }

// TunnelAdapter: detecta cloudflared/Tailscale.
type TunnelAdapter struct{}

func (TunnelAdapter) Name() string                      { return "tunnel" }
func (TunnelAdapter) Capabilities() map[Capability]bool { return inspectOnly }

// Registry son todos los adapters de infraestructura local conocidos por
// Asterion — usado por `asterion local doctor`/`local status` para listar
// capabilities de forma genérica en vez de mencionar cada adapter a mano.
func Registry() []Adapter {
	return []Adapter{UFWAdapter{}, SSHAdapter{}, ReverseProxyAdapter{}, TunnelAdapter{}}
}

// AssessSSHFirewallRisk es el único lugar donde este paquete "hace" algo
// más que declarar capabilities: delega en runtime.AssessSSHRisk, que es
// puro análisis de lectura (Inspect + Plan), nunca una mutación.
func AssessSSHFirewallRisk(ssh runtime.SSHInfo, fw runtime.FirewallInspection) runtime.SSHRiskAssessment {
	return runtime.AssessSSHRisk(ssh, fw)
}
