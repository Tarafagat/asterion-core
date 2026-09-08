package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"asterion-core/internal/adapters"
	"asterion-core/internal/adapters/aws"
	"asterion-core/internal/adapters/azure"
	"asterion-core/internal/adapters/gcp"
	"asterion-core/internal/adapters/oci"
	"asterion-core/internal/adapters/vercel"
	"asterion-core/internal/coreserver"
	"asterion-core/internal/localserve"
)

// coreCmd agrupa el servicio de Provider Adapters (AWS/Azure/GCP/OCI/Vercel)
// bajo el mismo binario `asterion` — mismo criterio que `local serve` para el
// dashboard: un solo binario para todo lo que un operador necesita correr,
// en vez de tener que acordarse de un segundo ejecutable (`asterion-core`,
// `cmd/asterion-core`) aparte. Ese binario standalone sigue existiendo (útil
// para, por ejemplo, una imagen de contenedor mínima con solo este
// servicio) — este subcomando es la misma lógica, expuesta también acá.
func coreCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "core",
		Short: "Servicio de Provider Adapters (AWS/Azure/GCP/OCI/Vercel) — lo consumen el CLI y Asterion Cloud (CORE_SERVICE_URL)",
	}
	root.AddCommand(coreServeCmd(), coreStopCmd(), coreRestartCmd(), coreStatusCmd())
	return root
}

const defaultCoreAddr = ":8090"

func resolveDefaultCoreAddr() string {
	if addr := os.Getenv("ASTERION_CORE_ADDR"); addr != "" {
		return addr
	}
	return defaultCoreAddr
}

func coreServeCmd() *cobra.Command {
	var addr string
	var background bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Levanta el servicio de Provider Adapters",
		Long: "Expone por HTTP los Provider Adapters (AWS/Azure/GCP/OCI/Vercel) — capabilities, discovery\n" +
			"y las operaciones de aprovisionamiento que ya estén implementadas. Es lo que 'asterion\n" +
			"providers'/'asterion capabilities' consultan localmente, y lo mismo que Asterion Cloud\n" +
			"espera en CORE_SERVICE_URL.\n\n" +
			"Por default corre en primer plano (Ctrl-C para parar) — para un deploy real seguí\n" +
			"administrándolo con systemd/tu gestor de procesos, igual que cualquier otro servicio\n" +
			"(ver el README, sección 'Levantar asterion-core'). --background es para dejarlo\n" +
			"corriendo rápido en esta misma máquina sin armar una unit de systemd (ej. para probar\n" +
			"algo local, o una máquina sin systemd) — parar con 'asterion core stop', reiniciar\n" +
			"después de reconstruir el binario con 'asterion core restart'.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if background {
				if _, alive, statusErr := localserve.Status(localserve.CoreServeName); statusErr == nil && alive {
					return fmt.Errorf("asterion-core ya está corriendo en segundo plano — 'asterion core stop' primero si querés reiniciarlo (o usá 'asterion core restart')")
				}
				return runCoreBackground(addr)
			}

			registry := adapters.NewRegistry(aws.New(), azure.New(), gcp.New(), oci.New(), vercel.New())
			server := coreserver.New(registry)
			fmt.Printf("asterion-core escuchando en %s (proveedores: %v)\n", addr, registry.Codes())
			return http.ListenAndServe(addr, server)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", resolveDefaultCoreAddr(), "Dirección donde escuchar (default también configurable con ASTERION_CORE_ADDR)")
	cmd.Flags().BoolVarP(&background, "background", "b", false, "Dejarlo corriendo en segundo plano en vez de bloquear la terminal")
	return cmd
}

// runCoreBackground re-ejecuta este mismo binario con 'core serve' (sin
// --background, para no recursar) como proceso desvinculado — mismo
// mecanismo que runBackendCoreBackground en local.go (Setsid+Release), pero
// acá no hace falta un intérprete de Python aparte: asterion-core es Go,
// el propio binario de 'asterion' ya lo sabe servir.
func runCoreBackground(addr string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("no pude resolver la ruta de este binario: %w", err)
	}

	logPath, err := localserve.LogPath(localserve.CoreServeName)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	run := exec.Command(self, "core", "serve", "--addr", addr)
	run.Env = os.Environ()
	run.Stdout = logFile
	run.Stderr = logFile
	localserve.SetDetached(run)

	if err := run.Start(); err != nil {
		return fmt.Errorf("no pude arrancar asterion-core: %w", err)
	}
	pid := run.Process.Pid
	_ = run.Process.Release()

	if err := localserve.SaveState(localserve.CoreServeName, localserve.State{
		PID: pid, Port: portFromAddr(addr), LogPath: logPath, StartedAt: time.Now(),
	}); err != nil {
		return fmt.Errorf("el proceso arrancó (pid %d) pero no pude guardar su estado: %w", pid, err)
	}

	fmt.Printf("✓ asterion-core corriendo en segundo plano — %s (pid %d)\n", addr, pid)
	fmt.Printf("  logs: %s\n", logPath)
	fmt.Println("  detenerlo: asterion core stop")
	return nil
}

// portFromAddr extrae el puerto de un ":8090"/"0.0.0.0:8090" para guardarlo
// en el State (solo informativo, para 'asterion core status') — un addr
// raro que no separe host:puerto se guarda como 0, sin romper el arranque.
func portFromAddr(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return 0
	}
	return port
}

func coreStopCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Detiene asterion-core arrancado con 'core serve --background'",
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := localserve.Stop(localserve.CoreServeName)
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(state)
				return nil
			}
			fmt.Printf("✓ asterion-core detenido (pid %d)\n", state.PID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

// coreRestartCmd equivale a 'core stop' + 'core serve --background' en un
// solo paso, reusando la misma dirección si ya estaba corriendo — mismo
// criterio que localRestartCmd. Sirve típicamente después de reconstruir
// el binario (un adapter nuevo, un fix) para que el proceso de fondo quede
// corriendo con el código nuevo sin tener que acordarse de los dos pasos.
func coreRestartCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Para y vuelve a levantar asterion-core en segundo plano, en un solo paso",
		Long: "Equivale a 'asterion core stop' + 'asterion core serve --background'. Si ya estaba\n" +
			"corriendo, reusa la misma dirección (salvo que pases --addr); si no estaba corriendo,\n" +
			"lo arranca directamente — mismo criterio que 'systemctl restart' con el servicio parado.\n\n" +
			"Si asterion-core corre administrado por systemd/tu gestor de procesos (el caso normal en\n" +
			"un deploy real), este comando NO lo toca — solo conoce los procesos que arrancó él mismo\n" +
			"con --background. Reiniciá el servicio real con las herramientas de tu gestor de procesos.",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedAddr := addr
			if state, alive, _ := localserve.Status(localserve.CoreServeName); alive {
				if resolvedAddr == "" {
					resolvedAddr = fmt.Sprintf(":%d", state.Port)
				}
				fmt.Printf("Deteniendo asterion-core (pid %d)...\n", state.PID)
				if _, err := localserve.Stop(localserve.CoreServeName); err != nil {
					return err
				}
				waitForPortFree(state.Port, 5*time.Second)
			} else {
				fmt.Println("asterion-core no estaba corriendo en segundo plano — arrancándolo.")
			}
			if resolvedAddr == "" {
				resolvedAddr = resolveDefaultCoreAddr()
			}
			return runCoreBackground(resolvedAddr)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "Dirección donde escuchar (default: la misma de antes si ya estaba corriendo, si no ASTERION_CORE_ADDR/:8090)")
	return cmd
}

func coreStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Si asterion-core está corriendo en segundo plano (arrancado con 'core serve --background')",
		RunE: func(cmd *cobra.Command, args []string) error {
			state, running, err := localserve.Status(localserve.CoreServeName)
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(map[string]any{"running": running, "state": state})
				return nil
			}
			if !running {
				fmt.Println("asterion-core no está corriendo en segundo plano (esto no dice nada sobre una instancia administrada por systemd/tu gestor de procesos).")
				return nil
			}
			fmt.Printf("✓ asterion-core corriendo — pid %d, puerto %d\n", state.PID, state.Port)
			fmt.Printf("  logs: %s\n", state.LogPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}
