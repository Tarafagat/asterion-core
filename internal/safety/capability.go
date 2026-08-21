// Package safety es el sistema de capabilities de los adapters de
// infraestructura local (firewall, SSH, reverse proxy, tunnel, TLS,
// systemd) — el equivalente, para el Runtime Engine, de lo que
// internal/capabilities es para los Provider Adapters de nube: cada
// adapter declara explícitamente qué sabe hacer, y el resto del sistema
// nunca asume una capacidad que no fue declarada.
//
// La regla central (spec "Infrastructure Safety Lab" §14): un adapter
// nunca puede aplicar un cambio (Apply) si no declaró también cómo
// deshacerlo (Rollback) — ver RequireSafeApply. Por eso hoy, en esta
// primera fase, todos los adapters de este paquete son de solo lectura:
// declaran Detect/Inspect/Plan, y Apply/Rollback quedan explícitamente
// UNSUPPORTED hasta que exista un Safety Lab real donde probarlos (ver
// internal/lab) — nunca se simula una capacidad que no se implementó.
package safety

import "fmt"

type Capability string

const (
	CapDetect   Capability = "detect"
	CapInspect  Capability = "inspect"
	CapPlan     Capability = "plan"
	CapApply    Capability = "apply"
	CapVerify   Capability = "verify"
	CapRollback Capability = "rollback"
)

// Adapter es cualquier componente que sepa administrar (aunque sea
// parcialmente) una pieza de infraestructura local.
type Adapter interface {
	Name() string
	Capabilities() map[Capability]bool
}

// Has responde false tanto si la capability es explícitamente false como
// si nunca se declaró — "no declarado" y "declarado como no soportado"
// son el mismo "no" para quien va a usar el adapter.
func Has(a Adapter, c Capability) bool {
	return a.Capabilities()[c]
}

// RequireCapability es la misma idea que RequireCapability de
// internal/adapters (Provider Adapters) aplicada acá: el Runtime Engine
// nunca debería poder invocar una operación que el adapter no declaró
// soportar.
func RequireCapability(a Adapter, c Capability) error {
	if !Has(a, c) {
		return fmt.Errorf("%s no declara la capability %q — operación no disponible", a.Name(), c)
	}
	return nil
}

// RequireSafeApply es el guard de seguridad central de todo el paquete:
// nunca permitir Apply sin Rollback declarado, sin excepciones. Preferir
// esto a llamar RequireCapability(a, CapApply) suelto en cualquier lugar
// que vaya a aplicar un cambio real.
func RequireSafeApply(a Adapter) error {
	if err := RequireCapability(a, CapApply); err != nil {
		return err
	}
	if !Has(a, CapRollback) {
		return fmt.Errorf(
			"%s declara 'apply' pero no 'rollback' — Asterion nunca aplica un cambio de infraestructura sin poder deshacerlo",
			a.Name(),
		)
	}
	return nil
}
