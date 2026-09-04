//go:build windows

package localserve

import (
	"os"
	"syscall"
)

// stillActive es el valor que GetExitCodeProcess devuelve mientras el
// proceso sigue corriendo — constante estable de la API de Win32
// (documentada en MSDN para GetExitCodeProcess), no expuesta como símbolo
// en el paquete syscall estándar de Go, así que se declara acá a mano.
const stillActive = 259

// isAlive confirma si pid sigue vivo abriendo un handle real al proceso
// (OpenProcess) y preguntándole su código de salida (GetExitCodeProcess) —
// el equivalente en Win32 de la señal 0 que usa isalive_unix.go en POSIX.
// Confirmado disponible en el syscall estándar de Go (sin sumar
// golang.org/x/sys) con `GOOS=windows go doc syscall.OpenProcess` /
// `syscall.GetExitCodeProcess` contra el toolchain real del repo.
func isAlive(pid int) (alive bool, checked bool) {
	if pid <= 0 {
		return false, true
	}
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		// No se pudo abrir el proceso — lo más común es que ya no exista.
		return false, true
	}
	defer syscall.CloseHandle(handle)

	var exitCode uint32
	if err := syscall.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false, true
	}
	return exitCode == stillActive, true
}

// signalStop no tiene un SIGTERM real en Windows, y no hay forma
// verificable sin una máquina Windows de que CTRL_BREAK_EVENT dispare un
// shutdown prolijo en uvicorn ahí — en vez de fingir esa garantía, corta
// directo con TerminateProcess (proc.Kill(), que Go ya mapea de forma
// portable). Limitación real y documentada, no un bug: en Windows
// 'asterion local stop' es un corte duro, no uno prolijo como en Unix.
func signalStop(proc *os.Process) {
	_ = proc.Kill()
}
