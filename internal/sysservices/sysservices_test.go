package sysservices

import "testing"

// TestValidAction no tiene build tag — las 3 acciones son el mismo
// concepto en los 3 sistemas operativos (ver ValidAction en
// sysservices.go), así que el test corre igual sin importar la
// plataforma de compilación.
func TestValidAction(t *testing.T) {
	for _, a := range []string{"start", "stop", "restart"} {
		if !ValidAction(a) {
			t.Errorf("ValidAction(%q) = false, want true", a)
		}
	}
	for _, a := range []string{"reload", "enable", "disable", "", "restart ; rm -rf /"} {
		if ValidAction(a) {
			t.Errorf("ValidAction(%q) = true, want false", a)
		}
	}
}
