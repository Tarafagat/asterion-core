package plugins

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// freePort le pide al sistema operativo un puerto TCP libre en loopback y
// lo suelta enseguida — la misma técnica que ya usa backend-core
// (find_free_port en app/main.py) para no tener que coordinar puertos a
// mano entre plugins. Es lo que hace "montar una API" instantáneo: nunca
// hay que decidir ni documentar un puerto por plugin.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// FreePort es freePort expuesto — lo usa `asterion plugin dev` para
// arrancar un plugin todavía no instalado en un puerto libre, con la misma
// técnica que usa Start para uno ya instalado.
func FreePort() (int, error) { return freePort() }

// WaitHealthy es waitHealthy expuesto — lo usa `asterion plugin dev` para
// esperar a que un plugin recién arrancado (no necesariamente instalado)
// responda su health check, igual que Start hace con uno instalado.
func WaitHealthy(port int, healthPath string, timeout time.Duration) error {
	return waitHealthy(port, healthPath, timeout)
}

func logPath(installed Installed) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, installed.Name+".log"), nil
}

// isAlive confirma si un proceso sigue vivo mandándole la señal 0 (no lo
// afecta, solo pregunta) — soportado en Unix. En Windows Go no expone un
// chequeo equivalente sin dependencias nuevas: en vez de asumir un estado
// que no se puede confirmar, se devuelve "no sé" explícito (false, false)
// — mismo criterio que ya usa el resto del sistema (sysinfo, safety) de
// nunca inventar un dato que no se pudo verificar.
func isAlive(pid int) (alive bool, checked bool) {
	if pid <= 0 {
		return false, true
	}
	if runtime.GOOS == "windows" {
		return false, false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, true
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil, true
}

// waitHealthy sondea GET http://127.0.0.1:<port><healthPath> hasta que
// responda 2xx o se cumpla el timeout — confirma que el plugin realmente
// levantó, no solo que exec.Command no devolvió error al instante (un
// proceso puede arrancar y morir medio segundo después).
func waitHealthy(port int, healthPath string, timeout time.Duration) error {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, healthPath)
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("%s respondió %d", url, resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("el plugin no respondió 2xx en %s dentro de %s: %w", url, timeout, lastErr)
}

// Start arranca el proceso de un plugin ya instalado. Le pasa su config
// (descifrada solo en este momento, en memoria, nunca a disco en texto
// plano) como variables de entorno — es el único canal por el que el
// plugin recibe sus secretos, nunca un archivo que el plugin mismo tenga
// que leer y potencialmente loguear por error.
func Start(name string) (Installed, error) {
	installed, err := Get(name)
	if err != nil {
		return Installed{}, err
	}

	if alive, checked := isAlive(installed.PID); checked && alive {
		return installed, fmt.Errorf("el plugin %q ya está corriendo (pid %d, puerto %d)", name, installed.PID, installed.Port)
	}

	// Releer el manifest desde disco antes de arrancar: 'start' es un punto
	// natural para resincronizar contra ediciones hechas directo al
	// plugin.yaml (ej. 'asterion plugin from-ast --force', o un --link
	// donde el autor sigue editando su propia carpeta) sin tener que
	// reinstalar. El manifest guardado en state.json es una foto de cuando
	// se instaló — sin este refresh, quedaría permanentemente desactualizado
	// para cualquier plugin que evolucione después de instalado. Si la
	// lectura falla (plugin.yaml roto en ese momento), se sigue con el
	// último manifest válido conocido en vez de bloquear el arranque.
	if fresh, err := LoadManifest(installed.Dir); err == nil {
		installed.Manifest = fresh
	}

	port := installed.Manifest.Port
	if port == 0 {
		port, err = freePort()
		if err != nil {
			return Installed{}, fmt.Errorf("no pude reservar un puerto libre: %w", err)
		}
	}

	config, err := GetConfig(name)
	if err != nil {
		return Installed{}, fmt.Errorf("no pude leer la config guardada de %q: %w", name, err)
	}

	env := os.Environ()
	env = append(env,
		"ASTERION_PLUGIN_NAME="+name,
		"ASTERION_PLUGIN_PORT="+strconv.Itoa(port),
		"ASTERION_PLUGIN_DIR="+installed.Dir,
	)
	for k, v := range config {
		envKey := "ASTERION_PLUGIN_CONFIG_" + strings.ToUpper(k)
		env = append(env, envKey+"="+v)
	}

	cmd := exec.Command(installed.Manifest.Start.Command, installed.Manifest.Start.Args...)
	cmd.Dir = installed.Dir
	cmd.Env = env

	logFile, err := logPath(installed)
	if err != nil {
		return Installed{}, err
	}
	out, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Installed{}, err
	}
	cmd.Stdout = out
	cmd.Stderr = out
	setDetached(cmd)

	if err := cmd.Start(); err != nil {
		out.Close()
		return Installed{}, fmt.Errorf("no pude arrancar %q: %w", installed.Manifest.Start.Command, err)
	}
	pid := cmd.Process.Pid
	// El proceso queda corriendo por su cuenta (setDetached lo desvincula
	// de esta sesión) — Release() suelta el handle sin esperarlo ni
	// matarlo, así el CLI puede terminar y el plugin sigue arriba.
	_ = cmd.Process.Release()
	out.Close()

	healthErr := waitHealthy(port, installed.Manifest.HealthPath, 10*time.Second)

	installed.Port = port
	installed.PID = pid
	if healthErr == nil {
		installed.Status = "running"
	} else {
		installed.Status = "unhealthy"
	}
	if err := Save(installed); err != nil {
		return installed, err
	}
	if healthErr != nil {
		return installed, fmt.Errorf("el proceso arrancó (pid %d) pero el health check falló — revisá %s: %w", pid, logFile, healthErr)
	}
	return installed, nil
}

// Stop manda SIGTERM al proceso del plugin y actualiza el estado. Si el
// pid guardado ya no corresponde a nada vivo, no es un error — el estado
// simplemente se corrige a "stopped" (el plugin pudo haberse caído solo).
func Stop(name string) error {
	installed, err := Get(name)
	if err != nil {
		return err
	}
	if installed.PID > 0 {
		if alive, checked := isAlive(installed.PID); checked && alive {
			proc, err := os.FindProcess(installed.PID)
			if err == nil {
				_ = proc.Signal(syscall.SIGTERM)
			}
		}
	}
	installed.Status = "stopped"
	installed.PID = 0
	return Save(installed)
}

// Status refresca (y devuelve) el estado real de un plugin instalado,
// reconciliando lo que dice state.json contra si el pid guardado sigue
// vivo de verdad — para que "running" en la UI nunca sea un dato viejo de
// un proceso que ya murió sin avisar.
func Status(name string) (Installed, error) {
	installed, err := Get(name)
	if err != nil {
		return Installed{}, err
	}
	if installed.Status != "running" && installed.Status != "unhealthy" {
		return installed, nil
	}
	alive, checked := isAlive(installed.PID)
	if !checked {
		return installed, nil // no se pudo confirmar (Windows) — se deja el último estado conocido
	}
	if !alive {
		installed.Status = "stopped"
		installed.PID = 0
		_ = Save(installed)
	}
	return installed, nil
}
