//go:build windows

package plugins

import "os/exec"

// En Windows no hay Setsid — el proceso queda ligado a esta consola por
// ahora. Levantar plugins como servicio de fondo real en Windows no está
// implementado todavía (mismo alcance que el resto del sistema: ver
// "instalar el agente" en el README, que hoy tampoco automatiza el
// servicio fuera de Linux).
func setDetached(cmd *exec.Cmd) {}
