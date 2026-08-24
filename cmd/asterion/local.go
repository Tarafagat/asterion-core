package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"asterion-core/internal/localauth"
	"asterion-core/internal/localserve"
	"asterion-core/internal/plugins"
	"asterion-core/internal/runtime"
	"asterion-core/internal/safety"
	"asterion-core/internal/sysinfo"
)

// localCmd responde preguntas sobre LA máquina donde corre el CLI —
// distinto de `instances` (un inventario de OTROS hosts) y de `cloud`
// (vincular con Asterion Cloud). Siempre devuelve datos crudos: nunca un
// costo. Cuánto sale ese consumo lo calcula el pricing engine de Asterion
// Cloud del lado del servidor, con la tarifa vigente del proyecto — este
// comando es la capa de "analista de sistema", no de facturación.
func localCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "local",
		Short: "Preguntas sobre esta máquina: qué es y cuánto está usando (datos crudos, sin costo)",
	}
	root.AddCommand(localInfoCmd(), localStatsCmd(), localServeCmd(), localStopCmd(), localStatusCmd(), localDoctorCmd(), localConfigCmd(), localAuthCmd())
	return root
}

func localStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Estado del Runtime: qué se detectó en esta máquina (systemd, firewall, reverse proxy, tunnel) y la configuración vigente",
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := runtime.Discover()
			if err != nil {
				return err
			}
			cfg, err := runtime.LoadConfig()
			if err != nil {
				return err
			}
			state, running, _ := localserve.Status()
			var localServe any
			if running {
				localServe = state
			}
			printJSON(map[string]any{
				"environment":         env,
				"config":              cfg,
				"local_serve":         localServe,
				"ssh":                 runtime.DiscoverSSH(),
				"network":             runtime.DiscoverNetwork(),
				"safety_capabilities": safetyCapabilities(),
			})
			return nil
		},
	}
}

// safetyCapabilities expone, por nombre de adapter, qué capabilities
// declara realmente (ver internal/safety) — para que "esto todavía no
// aplica cambios, solo detecta" sea visible desde la CLI, no algo que
// haya que leer en el código fuente.
func safetyCapabilities() map[string]map[string]bool {
	result := map[string]map[string]bool{}
	for _, adapter := range safety.Registry() {
		caps := map[string]bool{}
		for _, c := range []safety.Capability{
			safety.CapDetect, safety.CapInspect, safety.CapPlan,
			safety.CapApply, safety.CapVerify, safety.CapRollback,
		} {
			caps[string(c)] = safety.Has(adapter, c)
		}
		result[adapter.Name()] = caps
	}
	return result
}

func localDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Chequeo de salud del Runtime local (puerto, firewall, reverse proxy, tunnel, exposición)",
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := runtime.Discover()
			if err != nil {
				return err
			}
			cfg, err := runtime.LoadConfig()
			if err != nil {
				return err
			}
			applyLiveServicePort(&cfg)
			report := runtime.RunDoctor(env, cfg)
			printJSON(report)
			if !report.Healthy {
				return fmt.Errorf("asterion local doctor encontró problemas — ver el detalle arriba")
			}
			return nil
		},
	}
}

// applyLiveServicePort pisa cfg.ServicePort con el puerto real de una
// instancia de 'local serve --background' corriendo, si hay una. Sin esto,
// 'asterion local doctor' compara contra el puerto declarado en
// runtime.json (8091 por default) — que en modo --background es solo una
// PREFERENCIA, no una garantía: si 8091 ya estaba ocupado por otra cosa, el
// dashboard terminó escuchando en otro puerto, y doctor reportaría un falso
// negativo ("nada responde en 8091") aunque el dashboard esté sano.
func applyLiveServicePort(cfg *runtime.Config) {
	if state, running, _ := localserve.Status(); running && state.Port != 0 {
		cfg.ServicePort = state.Port
	}
}

func localConfigCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "config",
		Short: "Configuración persistente del Runtime local (~/.config/asterion/runtime.json)",
	}
	root.AddCommand(localConfigShowCmd(), localConfigGetCmd(), localConfigSetCmd())
	return root
}

func localConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Muestra la configuración del Runtime local",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := runtime.LoadConfig()
			if err != nil {
				return err
			}
			printJSON(cfg)
			return nil
		},
	}
}

func localConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Lee un valor puntual de la configuración (ej. service_port, remote_management_enabled)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := runtime.LoadConfig()
			if err != nil {
				return err
			}
			value, err := getConfigKey(cfg, args[0])
			if err != nil {
				return err
			}
			fmt.Println(value)
			return nil
		},
	}
}

func localConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Cambia un valor de la configuración y lo guarda",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := runtime.LoadConfig()
			if err != nil {
				return err
			}
			if err := setConfigKey(&cfg, args[0], args[1]); err != nil {
				return err
			}
			if err := runtime.SaveConfig(cfg); err != nil {
				return err
			}
			fmt.Printf("✓ %s = %s\n", args[0], args[1])
			return nil
		},
	}
}

// getConfigKey/setConfigKey soportan un conjunto fijo y chico de claves a
// propósito (no reflection genérica): son las mismas que expone
// runtime.Config, así el CLI nunca puede aceptar una clave que después la
// config no sepa interpretar.
func getConfigKey(cfg runtime.Config, key string) (string, error) {
	switch key {
	case "runtime_name":
		return cfg.RuntimeName, nil
	case "service_bind":
		return cfg.ServiceBind, nil
	case "service_port":
		return strconv.Itoa(cfg.ServicePort), nil
	case "metrics_enabled":
		return strconv.FormatBool(cfg.MetricsEnabled), nil
	case "metrics_interval_seconds":
		return strconv.Itoa(cfg.MetricsInterval), nil
	case "heartbeat_enabled":
		return strconv.FormatBool(cfg.HeartbeatEnabled), nil
	case "heartbeat_interval_seconds":
		return strconv.Itoa(cfg.HeartbeatInterval), nil
	case "remote_management_enabled":
		return strconv.FormatBool(cfg.RemoteManagement), nil
	default:
		return "", fmt.Errorf("clave desconocida: %q", key)
	}
}

func setConfigKey(cfg *runtime.Config, key, value string) error {
	switch key {
	case "runtime_name":
		cfg.RuntimeName = value
	case "service_bind":
		cfg.ServiceBind = value
	case "service_port":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("service_port debe ser un número: %w", err)
		}
		cfg.ServicePort = n
	case "metrics_enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		cfg.MetricsEnabled = b
	case "metrics_interval_seconds":
		n, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.MetricsInterval = n
	case "heartbeat_enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		cfg.HeartbeatEnabled = b
	case "heartbeat_interval_seconds":
		n, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.HeartbeatInterval = n
	case "remote_management_enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		cfg.RemoteManagement = b
	default:
		return fmt.Errorf("clave desconocida: %q", key)
	}
	return nil
}

// localServeCmd levanta el dashboard local: backend-core (FastAPI, Python)
// sirviendo su mini API y el build de frontend-core en un único puerto.
// Es un servicio DISTINTO de `asterion agent-run` — el agente empuja
// métricas a Asterion Cloud en segundo plano con la api-key de una
// instancia conectada; esto es un dashboard con login de Google, gateado al
// mismo correo de `asterion cloud login`, y no habla con Cloud para nada
// que no sea refrescar precios. Dos servicios, dos puertos, dos propósitos.
func localServeCmd() *cobra.Command {
	var dir string
	var port int
	var pythonBin string
	var background bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Levantar el dashboard local (backend-core + frontend-core) con un token propio, sin Google/Firebase",
		Long: "Arranca backend-core (Python/FastAPI) en un puerto libre, sirviendo su mini API y el\n" +
			"dashboard de frontend-core. Si backend-core/venv no existe todavía, se crea solo (python3 -m\n" +
			"venv venv && venv/bin/pip install -r requirements.txt) — no hace falta prepararlo a mano.\n" +
			"frontend-core sí necesita estar compilado por separado (pnpm build) — ver asterion-core/README.md.\n\n" +
			"El login ya no usa Google/Firebase: la primera vez que corrés esto se genera un token\n" +
			"propio (ver internal/localauth) que se imprime UNA sola vez acá abajo — pegalo en el\n" +
			"dashboard para entrar. Si ya existe uno de una corrida anterior, se reusa (no se vuelve a\n" +
			"mostrar: solo se guarda su hash). Perdiste el token: 'asterion local auth rotate'.\n\n" +
			"--background lo deja corriendo después de que este comando termine (mismo criterio que\n" +
			"'asterion plugin start'): parar con 'asterion local stop', o el botón de apagar del propio\n" +
			"dashboard (arriba a la derecha, una vez logueado).",
		RunE: func(cmd *cobra.Command, args []string) error {
			backendCoreDir, err := resolveBackendCoreDir(dir)
			if err != nil {
				return err
			}

			if background {
				if _, alive, statusErr := localserve.Status(); statusErr == nil && alive {
					return fmt.Errorf("el dashboard local ya está corriendo en segundo plano — 'asterion local stop' primero si querés reiniciarlo")
				}
			}

			token, isNew, err := localauth.EnsureToken()
			if err != nil {
				return fmt.Errorf("no se pudo preparar el token de acceso local: %w", err)
			}
			authPath, _ := localauth.Path()
			if isNew {
				fmt.Println(localauth.FormatFirstRun(token, authPath))
			} else {
				fmt.Printf("Usando el token de acceso ya generado (%s). 'asterion local auth rotate' genera uno nuevo si lo perdiste.\n", authPath)
			}

			python, err := ensureBackendCoreVenv(backendCoreDir, pythonBin)
			if err != nil {
				return err
			}

			if background {
				return runBackendCoreBackground(backendCoreDir, python, port)
			}

			run := exec.Command(python, "-m", "app.main")
			run.Dir = backendCoreDir
			run.Env = os.Environ()
			if port != 0 {
				run.Env = append(run.Env, fmt.Sprintf("BACKEND_CORE_PORT=%d", port))
			}
			run.Stdout = os.Stdout
			run.Stderr = os.Stderr
			run.Stdin = os.Stdin

			fmt.Printf("Levantando dashboard local desde %s ...\n", backendCoreDir)
			return run.Run()
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "Ruta a asterion-core/backend-core (default: la busca en ubicaciones comunes)")
	cmd.Flags().IntVar(&port, "port", 0, "Puerto fijo (default: el sistema elige uno libre)")
	cmd.Flags().StringVar(&pythonBin, "python", "", "Intérprete de Python 3 a usar si hay que crear el venv (default: 'python3' del PATH)")
	cmd.Flags().BoolVarP(&background, "background", "b", false, "Dejarlo corriendo en segundo plano en vez de bloquear la terminal")
	return cmd
}

// runBackendCoreBackground arranca backend-core desvinculado de esta
// sesión (mismo mecanismo que internal/plugins usa para plugins: Setsid +
// Release) y guarda pid/puerto/log en internal/localserve para que
// 'asterion local stop' (o el botón de apagar del dashboard) lo pueda
// encontrar después. El puerto lo elige Go de entrada — a diferencia del
// modo en primer plano, acá no hay terminal donde leer qué puerto anunció
// Python, así que Go se lo pasa siempre explícito por env var.
func runBackendCoreBackground(backendCoreDir, python string, explicitPort int) error {
	port := explicitPort
	if port == 0 {
		preferred := runtime.DefaultConfig().ServicePort
		if cfg, err := runtime.LoadConfig(); err == nil {
			preferred = cfg.ServicePort
		}
		p, err := preferredFreePort(preferred, 20)
		if err != nil {
			return fmt.Errorf("no pude reservar un puerto libre: %w", err)
		}
		port = p
	}

	logPath, err := localserve.LogPath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	run := exec.Command(python, "-m", "app.main")
	run.Dir = backendCoreDir
	run.Env = append(os.Environ(), fmt.Sprintf("BACKEND_CORE_PORT=%d", port))
	run.Stdout = logFile
	run.Stderr = logFile
	localserve.SetDetached(run)

	if err := run.Start(); err != nil {
		return fmt.Errorf("no pude arrancar backend-core: %w", err)
	}
	pid := run.Process.Pid
	_ = run.Process.Release()

	if err := localserve.SaveState(localserve.State{
		PID: pid, Port: port, LogPath: logPath, StartedAt: time.Now(),
	}); err != nil {
		return fmt.Errorf("el proceso arrancó (pid %d) pero no pude guardar su estado: %w", pid, err)
	}

	fmt.Printf("✓ Dashboard local corriendo en segundo plano — http://127.0.0.1:%d (pid %d)\n", port, pid)
	fmt.Printf("  logs: %s\n", logPath)
	fmt.Println("  detenerlo: asterion local stop (o el botón de apagar dentro del dashboard)")
	return nil
}

// preferredFreePort intenta preferred, preferred+1, ... hasta tries veces
// antes de resignarse a que el sistema operativo elija cualquier puerto
// libre — mismo criterio (y mismo rango) que find_free_port() en
// backend-core/app/main.py, para que el puerto en el que termina
// escuchando el dashboard en segundo plano sea el mismo que 'asterion
// local doctor'/'status' ya esperan encontrar (Config.ServicePort, default
// 8091) en el caso normal de una sola instancia corriendo. Si preferred ya
// está ocupado por otra cosa, tanto esto como Python se corren al
// siguiente — la única garantía real es "un puerto libre", nunca "EL
// puerto 8091 específicamente".
func preferredFreePort(preferred, tries int) (int, error) {
	for port := preferred; port < preferred+tries; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		l.Close()
		return port, nil
	}
	return plugins.FreePort()
}

// localStopCmd detiene el dashboard arrancado con 'local serve --background'.
func localStopCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Detiene el dashboard local arrancado con 'local serve --background'",
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := localserve.Stop()
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(state)
				return nil
			}
			fmt.Printf("✓ Dashboard local detenido (pid %d, puerto %d)\n", state.PID, state.Port)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

// ensureBackendCoreVenv devuelve la ruta al Python del venv de backend-core,
// creándolo (y sus dependencias) si todavía no existe — antes esto solo
// avisaba y caía al python3 del sistema, que casi nunca tiene fastapi
// instalado. pythonBin es el intérprete a usar SOLO para crear el venv
// (una vez creado, siempre se usa el de adentro del venv, nunca el del
// sistema, para no mezclar paquetes).
func ensureBackendCoreVenv(backendCoreDir, pythonBin string) (string, error) {
	venvDir := filepath.Join(backendCoreDir, "venv")
	venvPython := filepath.Join(venvDir, "bin", "python")
	if _, err := os.Stat(venvPython); err == nil {
		return venvPython, nil
	}

	if pythonBin == "" {
		pythonBin = "python3"
	}
	if _, err := exec.LookPath(pythonBin); err != nil {
		return "", fmt.Errorf("no encontré %q en el PATH para crear el entorno virtual de backend-core — instalá Python 3 o pasá --python /ruta/a/tu/python3", pythonBin)
	}

	fmt.Printf("No encontré backend-core/venv — creándolo con %s...\n", pythonBin)
	create := exec.Command(pythonBin, "-m", "venv", venvDir)
	create.Stdout = os.Stdout
	create.Stderr = os.Stderr
	if err := create.Run(); err != nil {
		return "", fmt.Errorf("no pude crear el entorno virtual: %w", err)
	}

	fmt.Println("Instalando dependencias (requirements.txt) — puede tardar un minuto la primera vez...")
	install := exec.Command(filepath.Join(venvDir, "bin", "pip"), "install", "-r", "requirements.txt")
	install.Dir = backendCoreDir
	install.Stdout = os.Stdout
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		_ = os.RemoveAll(venvDir) // no dejar un venv a medio instalar — el próximo 'serve' lo confundiría con uno completo
		return "", fmt.Errorf(
			"no pude instalar las dependencias de backend-core con %s: %w\n\n"+
				"Si el error de arriba menciona que tu versión de Python es demasiado nueva para alguna "+
				"dependencia (pydantic-core/PyO3 suele ser la primera en fallar), instalá una versión más "+
				"estable (ej. 'brew install python@3.13') y reintentá con --python /opt/homebrew/bin/python3.13",
			pythonBin, err,
		)
	}

	fmt.Println("✓ entorno virtual de backend-core listo")
	return venvPython, nil
}

// localAuthCmd administra el token de acceso al dashboard local — ver
// internal/localauth. Nunca expone el secreto salvo en el momento exacto
// en que se genera (acá, en 'rotate', o en 'serve' la primera vez).
func localAuthCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "auth",
		Short: "Token de acceso al dashboard local (~/.config/asterion/local-auth.yaml) — reemplaza el login con Google",
	}
	root.AddCommand(localAuthStatusCmd(), localAuthRotateCmd())
	return root
}

func localAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Si hay un token generado y desde cuándo — nunca muestra el secreto en sí",
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := localauth.GetStatus()
			if err != nil {
				return err
			}
			printJSON(status)
			return nil
		},
	}
}

func localAuthRotateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate",
		Short: "Genera un token nuevo e invalida el anterior (por si lo perdiste o se filtró)",
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := localauth.Rotate()
			if err != nil {
				return err
			}
			authPath, _ := localauth.Path()
			fmt.Println(localauth.FormatFirstRun(token, authPath))
			return nil
		},
	}
}

func resolveBackendCoreDir(explicit string) (string, error) {
	// Siempre se devuelve una ruta absoluta: exec.Command resuelve un
	// ejecutable con "/" en el path contra el cwd del proceso al momento de
	// arrancar, no contra cmd.Dir — una ruta relativa acá podía romperse
	// según desde dónde se invocara `go run`/el binario compilado.
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	if envDir := os.Getenv("ASTERION_BACKEND_CORE_DIR"); envDir != "" {
		return filepath.Abs(envDir)
	}
	candidates := []string{
		"backend-core",
		"asterion-core/backend-core",
		"../backend-core",
		"../asterion-core/backend-core",
	}
	for _, c := range candidates {
		if info, err := os.Stat(filepath.Join(c, "app", "main.py")); err == nil && !info.IsDir() {
			return filepath.Abs(c)
		}
	}
	return "", fmt.Errorf(
		"no encontré backend-core (probé %v desde el directorio actual) — corré este comando desde "+
			"la raíz del repo, o pasá --dir /ruta/a/asterion-core/backend-core",
		candidates,
	)
}

func localInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Qué es esta máquina: SO, arquitectura, CPU, RAM y disco totales, si es física/VM/contenedor",
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := sysinfo.GatherInfo()
			if err != nil {
				return err
			}
			printJSON(info)
			return nil
		},
	}
}

func localStatsCmd() *cobra.Command {
	var watch bool
	var interval time.Duration

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Cuánto está usando esta máquina ahora: CPU, RAM, disco y datos de red — sin costo",
		Long: "Datos crudos tal como los mediría cualquier herramienta de sistema (top, df, /proc/net/dev).\n" +
			"El costo de ese consumo es un cálculo aparte, del lado de Asterion Cloud, con la tarifa\n" +
			"vigente del proyecto una vez que la instancia esté conectada (ver 'asterion cloud connect').",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !watch {
				snap, err := sysinfo.Collect()
				if err != nil {
					return err
				}
				printJSON(snap)
				return nil
			}

			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				snap, err := sysinfo.Collect()
				if err != nil {
					return err
				}
				printJSON(snap)
				fmt.Println("---")
				<-ticker.C
			}
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "Repetir la medición periódicamente en vez de una sola vez")
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Second, "Intervalo entre mediciones con --watch")
	return cmd
}
