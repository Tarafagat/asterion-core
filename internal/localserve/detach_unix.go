//go:build !windows

package localserve

import (
	"os/exec"
	"syscall"
)

// SetDetached desvincula el proceso del dashboard de la sesión del CLI
// (Setsid) para que sobreviva a que `asterion local serve --background`
// termine — mismo criterio que internal/plugins.setDetached.
func SetDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
