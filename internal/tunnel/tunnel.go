// Package tunnel administra el proceso de fondo de `asterion local tunnel
// start`: dónde vive su estado (pid, puerto, URL pública, log), cómo se
// confirma si sigue vivo, y cómo se detiene. Misma forma exacta que
// internal/localserve — un estado en disco separado (tunnel.json, para no
// pisar local-serve.json: los dos procesos pueden convivir), señal 0 para
// preguntar si el pid sigue vivo, SIGTERM para pedirle que pare.
package tunnel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State es lo que se guarda mientras el túnel corre en segundo plano.
type State struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port,omitempty"` // 0 en modo --token (el mapeo hostname->puerto vive del lado de Cloudflare)
	URL       string    `json:"url,omitempty"`  // vacío hasta que cloudflared lo imprime (modo quick tunnel) o siempre vacío en modo --token
	Mode      string    `json:"mode"`           // "quick" | "token"
	LogPath   string    `json:"log_path"`
	StartedAt time.Time `json:"started_at"`
}

func baseDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "asterion")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func statePath() (string, error) {
	base, err := baseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tunnel.json"), nil
}

// LogPath es dónde queda el stdout/stderr de cloudflared — separado de
// state.json para poder tailearlo sin parsear JSON, y es de ahí de donde
// se lee la URL pública que cloudflared imprime al arrancar (modo quick).
func LogPath() (string, error) {
	base, err := baseDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "tunnel.log"), nil
}

func SaveState(s State) error {
	path, err := statePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func RemoveState() error {
	path, err := statePath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func LoadState() (State, bool, error) {
	path, err := statePath()
	if err != nil {
		return State{}, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, false, err
	}
	return s, true, nil
}

// isAlive: implementación real por SO, separada en isalive_unix.go/
// isalive_windows.go — mismo criterio que internal/localserve.

// Status reconcilia el estado guardado contra si el proceso realmente
// sigue vivo — si el pid guardado ya no existe, limpia el estado viejo.
func Status() (State, bool, error) {
	s, found, err := LoadState()
	if err != nil || !found {
		return State{}, false, err
	}
	alive, checked := isAlive(s.PID)
	if checked && !alive {
		_ = RemoveState()
		return State{}, false, nil
	}
	return s, true, nil
}

// Stop le pide al proceso de cloudflared que pare y borra el estado —
// signalStop (isalive_unix.go/isalive_windows.go) decide SIGTERM (Unix) o
// un corte duro vía TerminateProcess (Windows, sin garantía de shutdown
// prolijo verificable ahí).
func Stop() (State, error) {
	s, alive, err := Status()
	if err != nil {
		return State{}, err
	}
	if !alive {
		return State{}, fmt.Errorf("no hay ningún túnel corriendo en segundo plano")
	}
	if proc, err := os.FindProcess(s.PID); err == nil {
		signalStop(proc)
	}
	_ = RemoveState()
	return s, nil
}
