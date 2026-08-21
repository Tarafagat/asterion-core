// Package lab es el Asterion Infrastructure Safety Lab: la arquitectura
// para crear máquinas desechables donde probar cambios reales de
// infraestructura (firewall, SSH, reverse proxy, tunnel) antes de tocar
// producción.
//
// Estado real de esta implementación (léase antes de usar): ningún
// backend de virtualización quedó disponible en el entorno donde se
// escribió este paquete (sin Docker, sin QEMU instalado, sin root sin
// contraseña para instalarlo) — ver Backend.Available(). Por eso Create/
// List/Exec/Destroy de cada backend devuelven ErrNotImplemented: la
// interfaz y el flujo (spec §2: create → provision → configure → install
// agent → test → verify → rollback → verify → destroy) están completos y
// listos para implementarse backend por backend, pero ninguno ejecuta
// todavía una VM real. Implementarlo en falso (simular que "funcionó")
// sería peor que no tenerlo — el objetivo del Lab es justamente dar
// confianza real, no aparente.
package lab

import (
	"errors"
	"time"
)

// ErrNotImplemented es el mismo patrón que internal/adapters.ErrNotImplemented
// para los Provider Adapters de nube: la operación existe en la interfaz,
// pero este backend concreto todavía no la ejecuta de verdad.
var ErrNotImplemented = errors.New("lab: no implementado todavía en este backend")

// ErrBackendUnavailable se devuelve cuando Available() ya determinó que
// este backend no se puede usar en la máquina actual (falta el binario,
// falta /dev/kvm, etc.) — antes de siquiera intentar Create.
var ErrBackendUnavailable = errors.New("lab: backend no disponible en esta máquina")

type EnvironmentStatus string

const (
	StatusCreating  EnvironmentStatus = "creating"
	StatusRunning   EnvironmentStatus = "running"
	StatusDestroyed EnvironmentStatus = "destroyed"
	StatusFailed    EnvironmentStatus = "failed"
)

// Environment es una VM/contenedor desechable del laboratorio.
type Environment struct {
	ID        string            `json:"id"`
	Backend   string            `json:"backend"`
	Image     string            `json:"image"`
	Status    EnvironmentStatus `json:"status"`
	CreatedAt time.Time         `json:"created_at"`
}

// Spec describe qué entorno crear — deliberadamente chico por ahora
// (spec §1 pide Ubuntu + systemd + SSH + firewall + nginx/Caddy + Docker +
// Asterion Agent; qué imagen concreta trae todo eso es una decisión del
// backend, no de este contrato).
type Spec struct {
	Image string `json:"image"` // ej. "ubuntu-22.04" — interpretado por cada backend
}

// Backend es un proveedor de entornos desechables. Cada implementación
// debe ser honesta en Available(): decir que sí sin poder crear un
// entorno real es exactamente el tipo de mock que este paquete existe
// para evitar.
type Backend interface {
	Name() string
	// Available hace una comprobación real (binario en PATH, dispositivo
	// accesible, etc.) — nunca asume.
	Available() (ok bool, detail string)
	Create(spec Spec) (Environment, error)
	List() ([]Environment, error)
	Exec(id, command string) (output string, err error)
	Destroy(id string) error
}

// DetectBackend prueba los backends conocidos en orden de preferencia
// (Docker primero — más liviano y rápido para systemd-en-contenedor que
// una VM completa; QEMU/KVM como alternativa de VM real) y devuelve el
// primero disponible. Si ninguno lo está, el error agrega el detalle de
// cada uno para que el usuario sepa exactamente qué instalar.
func DetectBackend() (Backend, error) {
	candidates := []Backend{DockerBackend{}, QEMUBackend{}}
	var reasons []string
	for _, b := range candidates {
		if ok, detail := b.Available(); ok {
			return b, nil
		} else {
			reasons = append(reasons, b.Name()+": "+detail)
		}
	}
	msg := "ningún backend de laboratorio disponible en esta máquina.\n"
	for _, r := range reasons {
		msg += "  - " + r + "\n"
	}
	msg += "Instalá Docker o QEMU+KVM para poder crear entornos desechables (ver asterion-core/README.md § Safety Lab)."
	return nil, errors.New(msg)
}
