//go:build windows

package localserve

import (
	"os/exec"
	"syscall"
)

// detachedProcess desvincula el proceso hijo de la consola del padre —
// constante de Win32 (CreateProcess, dwCreationFlags) no expuesta como
// símbolo en el paquete syscall estándar de Go, se declara a mano.
const detachedProcess = 0x00000008

// SetDetached es el equivalente Windows de Setsid (ver detach_unix.go):
// CREATE_NEW_PROCESS_GROUP + DETACHED_PROCESS evita que un Ctrl+C en la
// terminal que lanzó 'local serve --background' se lleve puesto al
// proceso hijo, y que quede atado a la consola del padre.
func SetDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
	}
}
