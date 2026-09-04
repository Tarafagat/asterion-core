// Package localserve administra el proceso de fondo de `asterion local
// serve --background`: dónde vive su estado (pid, puerto, log), cómo se
// confirma si sigue vivo, y cómo se detiene. Mismo criterio exacto que
// internal/plugins/process.go+store.go usan para procesos de plugins —
// acá no se reusa ese paquete porque el dashboard local no es un plugin
// (no tiene manifest, no se instala/desinstala), pero la forma es
// deliberadamente la misma: un estado en disco, señal 0 para preguntar si
// el pid sigue vivo, SIGTERM para pedirle que pare.
package localserve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State es lo que se guarda mientras el dashboard corre en segundo plano.
// Se borra al detenerlo — a diferencia de internal/plugins, acá no hace
// falta un historial de "instalado pero parado": solo existe si está
// corriendo.
type State struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	LogPath   string    `json:"log_path"`
	StartedAt time.Time `json:"started_at"`
}

// BaseDir es ~/.config/asterion (o su equivalente por SO) — el mismo
// directorio que ya usan internal/cliconfig e internal/localauth.
func BaseDir() (string, error) {
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
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "local-serve.json"), nil
}

// LogPath es dónde queda el stdout/stderr del proceso de fondo — separado
// de state.json para poder tailearlo sin parsear JSON.
func LogPath() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "local-serve.log"), nil
}

// SaveState persiste el estado del proceso recién arrancado.
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

// RemoveState borra el estado — se llama al confirmar que el proceso paró.
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

// LoadState devuelve el estado guardado, si hay alguno.
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

// isAlive confirma si pid sigue vivo — implementación real por SO, separada
// en isalive_unix.go (señal 0, POSIX) e isalive_windows.go
// (OpenProcess+GetExitCodeProcess, Win32) — mismo criterio que SetDetached
// (ver detach_unix.go/detach_windows.go): el build tag elige la
// implementación correcta en tiempo de compilación, no un
// `if runtime.GOOS` en tiempo de ejecución.

// Status reconcilia el estado guardado contra si el proceso realmente
// sigue vivo — si el pid guardado ya no existe, limpia el estado viejo en
// vez de reportar algo corriendo que en realidad se cayó solo.
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

// Stop le pide al proceso de fondo que pare y borra el estado — la señal
// exacta la decide signalStop (isalive_unix.go/isalive_windows.go): en
// Unix es SIGTERM (uvicorn lo maneja de forma prolija, la misma señal que
// recibiría con Ctrl-C en primer plano); en Windows es un corte duro
// (TerminateProcess vía proc.Kill()) — no hay forma verificable sin una
// máquina Windows real de que un shutdown "prolijo" ahí funcione, así que
// no se finge esa garantía.
func Stop() (State, error) {
	s, alive, err := Status()
	if err != nil {
		return State{}, err
	}
	if !alive {
		return State{}, fmt.Errorf("no hay ningún dashboard local corriendo en segundo plano")
	}
	if proc, err := os.FindProcess(s.PID); err == nil {
		signalStop(proc)
	}
	_ = RemoveState()
	return s, nil
}
