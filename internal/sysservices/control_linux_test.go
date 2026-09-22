//go:build linux

package sysservices

import (
	"strings"
	"testing"
)

func TestValidUnitName(t *testing.T) {
	cases := map[string]bool{
		"nginx.service":                 true,
		"asterion-agent-inst_1.service": true,
		"my.app@1.service":              true,
		"UPPER.service":                 true,
		"":                              false,
		"nginx":                         false, // sin .service
		"-nginx.service":                false, // no puede empezar con '-'
		"nginx; rm -rf /.service":       false, // espacios/símbolos de shell
		"../etc/passwd.service":         false,
		"nginx.service; echo pwned":     false,
	}
	for name, want := range cases {
		if got := ValidUnitName(name); got != want {
			t.Errorf("ValidUnitName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestValidUnitName_RespectsSystemdMaxLength(t *testing.T) {
	// 255 es el límite real de systemd para el nombre total de una unidad
	// (incluido el ".service"): 1 char requerido + hasta 246 opcionales +
	// ".service" (8) = 255 como máximo válido.
	atLimit := strings.Repeat("a", 247) + ".service" // 255 en total
	if !ValidUnitName(atLimit) {
		t.Errorf("un nombre de %d caracteres totales (el límite) debería ser válido", len(atLimit))
	}
	overLimit := strings.Repeat("a", 248) + ".service" // 256 en total
	if ValidUnitName(overLimit) {
		t.Errorf("un nombre de %d caracteres totales (uno más del límite) no debería ser válido", len(overLimit))
	}
}

func TestIsProtected(t *testing.T) {
	cases := map[string]bool{
		"ssh.service":                     true,
		"sshd.service":                    true,
		"networking.service":              true,
		"systemd-networkd.service":        true,
		"NetworkManager.service":          true,
		"asterion-agent-inst_abc.service": true, // por prefijo
		"nginx.service":                   false,
		"mysql.service":                   false,
	}
	for name, want := range cases {
		if got := IsProtected(name); got != want {
			t.Errorf("IsProtected(%q) = %v, want %v", name, got, want)
		}
	}
}
