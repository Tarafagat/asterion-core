//go:build !windows

package plugins

import (
	"os/exec"
	"syscall"
)

// setDetached desvincula el proceso del plugin de la sesión del CLI
// (Setsid: arranca su propio grupo de procesos) para que sobreviva a que
// `asterion plugin start` termine — un plugin es un servicio de larga
// duración, no un comando que corre y se va.
func setDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
