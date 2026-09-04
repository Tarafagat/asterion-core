//go:build windows

package tunnel

import (
	"os"
	"syscall"
)

// stillActive — ver el comentario en internal/localserve/isalive_windows.go
// (misma constante, mismo motivo: no está expuesta en el paquete syscall
// estándar de Go).
const stillActive = 259

// isAlive confirma si pid sigue vivo vía OpenProcess+GetExitCodeProcess —
// ver internal/localserve/isalive_windows.go para el detalle completo,
// misma implementación acá porque internal/tunnel no importa
// internal/localserve (paquetes hermanos, sin dependencia entre sí).
func isAlive(pid int) (alive bool, checked bool) {
	if pid <= 0 {
		return false, true
	}
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false, true
	}
	defer syscall.CloseHandle(handle)

	var exitCode uint32
	if err := syscall.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false, true
	}
	return exitCode == stillActive, true
}

// signalStop corta cloudflared con TerminateProcess (proc.Kill()) — sin
// SIGTERM real en Windows, ver el mismo comentario en
// internal/localserve/isalive_windows.go.
func signalStop(proc *os.Process) {
	_ = proc.Kill()
}
