//go:build !windows

package tunnel

import (
	"os"
	"syscall"
)

// isAlive confirma si pid sigue vivo con señal 0 (POSIX) — igual técnica
// que internal/localserve.isAlive.
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

// signalStop le pide a cloudflared que pare con SIGTERM.
func signalStop(proc *os.Process) {
	_ = proc.Signal(syscall.SIGTERM)
}
