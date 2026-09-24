package plugins

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// fakePluginSrc es un "plugin" real y mínimo: un servidor HTTP que escucha
// en el puerto que Asterion le pasa por ASTERION_PLUGIN_PORT (el mismo
// canal que usa Start en producción) y responde 2xx en /health. Se compila
// una sola vez (ver TestMain) y se reusa en todos los tests de este
// archivo — la idea es ejercitar el ciclo de vida real (exec.Command, bind
// de puerto real, health check real) en vez de mockear cualquiera de esas
// piezas, porque el bug que estos tests cubren (el puerto cambia en cada
// restart) solo existe en la interacción real entre todas ellas.
const fakePluginSrc = `package main

import (
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("ASTERION_PLUGIN_PORT")
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	http.ListenAndServe("127.0.0.1:"+port, nil)
}
`

var fakePluginBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "asterion-fakeplugin-*")
	if err != nil {
		fmt.Println("no pude crear el dir temporal para el plugin fake:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakePluginSrc), 0o644); err != nil {
		fmt.Println("no pude escribir el plugin fake:", err)
		os.Exit(1)
	}
	bin := filepath.Join(dir, "fakeplugin")
	build := exec.Command("go", "build", "-o", bin, src)
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Printf("no pude compilar el plugin fake: %v\n%s\n", err, out)
		os.Exit(1)
	}
	fakePluginBin = bin

	os.Exit(m.Run())
}

// isolatedHome apunta BaseDir() (vía os.UserConfigDir) a un directorio
// temporal propio del test, para que estos tests nunca toquen los plugins
// de verdad instalados en la máquina donde corren.
func isolatedHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "") // no interferir en Linux si estuviera seteada
}

// testInstalled arma (y guarda) un Installed mínimo pero real — puerto
// dinámico (0, el caso común que dispara el bug), Start apuntando al
// binario fake. Dir apunta a un directorio sin plugin.yaml a propósito:
// Start() intenta releer el manifest desde ahí y, al fallar, sigue con el
// manifest ya guardado (ver el comentario en Start) — mismo camino que
// toma cualquier plugin cuyo plugin.yaml no está disponible en ese
// instante, así que vale la pena que el test pase por ahí también.
func testInstalled(t *testing.T, name string) Installed {
	t.Helper()
	installed := Installed{
		ExternalRef: NewExternalRef(),
		Name:        name,
		Dir:         t.TempDir(),
		Manifest: Manifest{
			Name:       name,
			Version:    "0.0.0-test",
			Start:      StartSpec{Command: fakePluginBin},
			Port:       0,
			HealthPath: "/health",
		},
		Status: "stopped",
	}
	if err := Save(installed); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	return installed
}

// reap junta el proceso del plugin apenas termina, en vez de dejarlo
// zombie hasta que el propio test (que es un proceso de larga vida, a
// diferencia del CLI real de `asterion` que termina casi al toque después
// de Start/Restart) lo recolecte solo. Sin esto, isAlive() sigue viendo
// "vivo" al zombie hasta que algo lo reapea, y waitExit() se queda
// esperando el timeout completo en cada ciclo — no es un bug de Stop()/
// Restart(), es un artefacto de que el proceso de test nunca hace wait().
func reap(pid int) {
	if pid <= 0 {
		return
	}
	go func() {
		if p, err := os.FindProcess(pid); err == nil {
			_, _ = p.Wait()
		}
	}()
}

func getHealth(t *testing.T, port int) int {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		t.Fatalf("GET /health en el puerto %d falló: %v", port, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestPortAvailable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no pude reservar un puerto para el test: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port

	if portAvailable(port) {
		t.Errorf("portAvailable(%d) = true mientras el puerto está ocupado, want false", port)
	}

	l.Close()

	if !portAvailable(port) {
		t.Errorf("portAvailable(%d) = false después de liberarlo, want true", port)
	}
}

// TestStart_ReusesLastKnownPort es el test directo del bug reportado:
// parar un plugin y volver a arrancarlo NO debe cambiarle el puerto
// cuando el manifest usa puerto dinámico (Port: 0, el caso común).
func TestStart_ReusesLastKnownPort(t *testing.T) {
	isolatedHome(t)
	testInstalled(t, "fake-plugin-reuse")

	first, err := Start("fake-plugin-reuse")
	if err != nil {
		t.Fatalf("Start() #1 = %v", err)
	}
	reap(first.PID)
	if got := getHealth(t, first.Port); got != http.StatusOK {
		t.Fatalf("health check tras Start() #1 = %d, want 200", got)
	}

	if err := Stop("fake-plugin-reuse"); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	waitExit(first.PID, 5*time.Second)

	second, err := Start("fake-plugin-reuse")
	if err != nil {
		t.Fatalf("Start() #2 = %v", err)
	}
	reap(second.PID)

	if second.Port != first.Port {
		t.Errorf("el puerto cambió de %d a %d tras stop+start — el bug reportado sigue presente", first.Port, second.Port)
	}
	if second.PID == first.PID {
		t.Errorf("Start() #2 devolvió el mismo pid (%d) que #1 — debería ser un proceso nuevo", second.PID)
	}
	if got := getHealth(t, second.Port); got != http.StatusOK {
		t.Fatalf("health check tras Start() #2 = %d, want 200", got)
	}

	_ = Stop("fake-plugin-reuse")
}

// TestRestart_KeepsSamePortAcrossMultipleCycles cubre 'asterion plugin
// restart' específicamente: varios ciclos seguidos, mismo puerto siempre,
// pid distinto cada vez (confirma que es un proceso nuevo de verdad, no
// estado viejo devuelto sin más).
func TestRestart_KeepsSamePortAcrossMultipleCycles(t *testing.T) {
	isolatedHome(t)
	testInstalled(t, "fake-plugin-restart")

	first, err := Start("fake-plugin-restart")
	if err != nil {
		t.Fatalf("Start() = %v", err)
	}
	reap(first.PID)

	lastPID := first.PID
	for i := 1; i <= 3; i++ {
		got, err := Restart("fake-plugin-restart")
		if err != nil {
			t.Fatalf("Restart() ciclo %d = %v", i, err)
		}
		reap(got.PID)
		if got.Port != first.Port {
			t.Errorf("ciclo %d: puerto = %d, want %d (el original)", i, got.Port, first.Port)
		}
		if got.PID == lastPID {
			t.Errorf("ciclo %d: pid = %d, igual al de la corrida anterior — debería ser un proceso nuevo", i, got.PID)
		}
		if code := getHealth(t, got.Port); code != http.StatusOK {
			t.Fatalf("ciclo %d: health check = %d, want 200", i, code)
		}
		lastPID = got.PID
	}

	_ = Stop("fake-plugin-restart")
}

// TestStart_FallsBackToNewPortWhenLastKnownPortTaken confirma el otro
// lado del fix: si el último puerto conocido ya no está libre (otro
// proceso lo tomó mientras el plugin estaba parado), Start() no falla —
// pide uno nuevo, igual que hacía antes de este fix para el primer
// arranque.
func TestStart_FallsBackToNewPortWhenLastKnownPortTaken(t *testing.T) {
	isolatedHome(t)
	testInstalled(t, "fake-plugin-fallback")

	first, err := Start("fake-plugin-fallback")
	if err != nil {
		t.Fatalf("Start() #1 = %v", err)
	}
	reap(first.PID)
	if err := Stop("fake-plugin-fallback"); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	waitExit(first.PID, 5*time.Second)

	blocker, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", first.Port))
	if err != nil {
		t.Fatalf("no pude ocupar el puerto %d para el test: %v", first.Port, err)
	}
	defer blocker.Close()

	second, err := Start("fake-plugin-fallback")
	if err != nil {
		t.Fatalf("Start() #2 = %v", err)
	}
	reap(second.PID)
	if second.Port == first.Port {
		t.Fatalf("Start() #2 devolvió el puerto %d, que este test tiene ocupado a propósito", second.Port)
	}
	if got := getHealth(t, second.Port); got != http.StatusOK {
		t.Fatalf("health check tras el fallback = %d, want 200", got)
	}

	_ = Stop("fake-plugin-fallback")
}
