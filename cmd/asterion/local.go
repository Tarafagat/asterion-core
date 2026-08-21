package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"asterion-core/internal/localauth"
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
	root.AddCommand(localInfoCmd(), localStatsCmd(), localServeCmd(), localStatusCmd(), localDoctorCmd(), localConfigCmd(), localAuthCmd())
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
			printJSON(map[string]any{
				"environment":         env,
				"config":              cfg,
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
			report := runtime.RunDoctor(env, cfg)
			printJSON(report)
			if !report.Healthy {
				return fmt.Errorf("asterion local doctor encontró problemas — ver el detalle arriba")
			}
			return nil
		},
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

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Levantar el dashboard local (backend-core + frontend-core) con un token propio, sin Google/Firebase",
		Long: "Arranca backend-core (Python/FastAPI) en un puerto libre, sirviendo su mini API y el\n" +
			"dashboard de frontend-core. Requiere que asterion-core/backend-core tenga sus dependencias\n" +
			"instaladas (python3 -m venv venv && venv/bin/pip install -r requirements.txt) y que\n" +
			"frontend-core esté compilado (pnpm build) — ver asterion-core/README.md.\n\n" +
			"El login ya no usa Google/Firebase: la primera vez que corrés esto se genera un token\n" +
			"propio (ver internal/localauth) que se imprime UNA sola vez acá abajo — pegalo en el\n" +
			"dashboard para entrar. Si ya existe uno de una corrida anterior, se reusa (no se vuelve a\n" +
			"mostrar: solo se guarda su hash). Perdiste el token: 'asterion local auth rotate'.",
		RunE: func(cmd *cobra.Command, args []string) error {
			backendCoreDir, err := resolveBackendCoreDir(dir)
			if err != nil {
				return err
			}

			token, isNew, err := localauth.EnsureToken()
			if err != nil {
				return fmt.Errorf("no se pudo preparar el token de acceso local: %w", err)
			}
			if isNew {
				fmt.Println(localauth.FormatFirstRun(token))
			} else {
				fmt.Println("Usando el token de acceso ya generado (~/.config/asterion/local-auth.yaml). 'asterion local auth rotate' genera uno nuevo si lo perdiste.")
			}

			python := filepath.Join(backendCoreDir, "venv", "bin", "python")
			if _, err := os.Stat(python); err != nil {
				python = "python3"
				fmt.Println("aviso: no encontré backend-core/venv — usando python3 del sistema (puede faltar instalar dependencias)")
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
	return cmd
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
			fmt.Println(localauth.FormatFirstRun(token))
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
