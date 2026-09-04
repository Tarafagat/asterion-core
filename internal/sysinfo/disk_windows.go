//go:build windows

package sysinfo

import "fmt"

// diskGB no tiene implementación real en Windows todavía (ver el
// comentario en sysinfo.go: métricas de Windows quedaron explícitamente
// fuera de esta vuelta) — este stub existe solo para que el paquete
// compile ahí. Nunca se llega a llamar en la práctica: Snapshot()/Info()
// ya rechazan Windows antes de intentar leer disco.
func diskGB(path string) (usedGB, totalGB float64, err error) {
	return 0, 0, fmt.Errorf("sysinfo todavía no soporta windows")
}
