//go:build !windows

package localserve

import (
	"os"
	"syscall"
)

// isAlive confirma si pid sigue vivo con señal 0 (POSIX) — igual técnica
// que internal/plugins.isAlive.
func isAlive(pid int) (alive bool, checked bool) {
	if pid <= 0 {
		return false, true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, true
	}
	return proc.Signal(syscall.Signal(0)) == nil, true
}

// signalStop le pide al proceso que pare con SIGTERM — uvicorn (backend-core)
// lo maneja de forma prolija, la misma señal que recibiría con Ctrl-C en
// primer plano.
func signalStop(proc *os.Process) {
	_ = proc.Signal(syscall.SIGTERM)
}
