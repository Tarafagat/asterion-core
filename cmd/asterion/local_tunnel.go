package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"asterion-core/internal/localserve"
	"asterion-core/internal/nginxtunnel"
	"asterion-core/internal/plugins"
	"asterion-core/internal/tunnel"
)

// localTunnelCmd expone un puerto local con una URL pública, vía uno de
// varios "tunnel providers" (--tp):
//
//   - "cloudflare" (default): Cloudflare Tunnel — cloudflared arranca como
//     proceso propio de Asterion (PID que se mata para parar). Dos modos:
//     "quick" (URL random gratis, sin cuenta) o "token" (un túnel con
//     nombre creado a mano en el dashboard de Cloudflare, con tu propio
//     dominio — TLS lo maneja Cloudflare, nunca hace falta certificado
//     propio).
//   - "nginx": un nginx YA instalado en esta máquina — Asterion agrega su
//     propio server block (nunca toca otros sites) y hace reload. Necesita
//     --domain (dominio público que ya apunte acá) y, opcionalmente,
//     --email para TLS automático real vía certbot.
//
// Un solo túnel a la vez, sin importar qué provider lo levantó — mismo
// archivo de estado (tunnel.json) para todos.
func localTunnelCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "tunnel",
		Short: "Expone un puerto local con una URL pública, vía Cloudflare Tunnel o Nginx (--tp)",
	}
	root.AddCommand(localTunnelStartCmd(), localTunnelStopCmd(), localTunnelStatusCmd(), localTunnelConfigCmd())
	return root
}

func findCloudflared() (string, error) {
	path, err := exec.LookPath("cloudflared")
	if err == nil {
		return path, nil
	}
	return "", fmt.Errorf(
		"no encontré 'cloudflared' en el PATH — instalalo primero:\n" +
			"  curl -L --output cloudflared.deb https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb\n" +
			"  sudo dpkg -i cloudflared.deb\n" +
			"(en macOS: brew install cloudflared)",
	)
}

// resolveTunnelPort decide qué puerto exponer cuando 'start' no recibió
// --port explícito, en orden: --plugin (si se pasó, exige que esté
// corriendo) -> el plugin principal si hay uno y está corriendo -> el
// puerto de 'local serve' si está corriendo (comportamiento de siempre)
// -> error. Nunca adivina: cada paso confirma el estado real antes de
// usarlo. Aplica sin importar el --tp elegido — todos necesitan un
// puerto local real al que apuntar.
func resolveTunnelPort(pluginFlag string) (port int, source string, err error) {
	if pluginFlag != "" {
		installed, err := plugins.Get(pluginFlag)
		if err != nil {
			return 0, "", err
		}
		if installed.Status != "running" || installed.Port == 0 {
			return 0, "", fmt.Errorf("el plugin %q no está corriendo (asterion plugin start %s)", pluginFlag, pluginFlag)
		}
		return installed.Port, fmt.Sprintf("plugin %q", pluginFlag), nil
	}

	if main, found, err := plugins.GetMain(); err != nil {
		return 0, "", err
	} else if found && main.Status == "running" && main.Port != 0 {
		return main.Port, fmt.Sprintf("plugin principal %q", main.Name), nil
	}

	if s, alive, err := localserve.Status(localserve.LocalServeName); err != nil {
		return 0, "", err
	} else if alive {
		return s.Port, "local serve", nil
	}

	return 0, "", fmt.Errorf(
		"no encontré qué exponer: no pasaste --port ni --plugin, no hay ningún plugin principal " +
			"corriendo (ver 'asterion plugin set-main'), y no hay ningún 'asterion local serve' corriendo",
	)
}

// isAnyTunnelAlive reconcilia el estado guardado contra la realidad, sin
// importar qué provider lo levantó — cloudflare se chequea por PID
// (tunnel.Status, ya probado), nginx por si su server block sigue
// habilitado (nginxtunnel.IsAlive, ver ese paquete: no hay PID propio).
func isAnyTunnelAlive() (bool, error) {
	s, found, err := tunnel.LoadState()
	if err != nil || !found {
		return false, err
	}
	if s.ResolveProvider() == "nginx" {
		return nginxtunnel.IsAlive(), nil
	}
	_, alive, err := tunnel.Status()
	return alive, err
}

// startCloudflareTunnel es exactamente la lógica que ya existía antes de
// que hubiera más de un provider — sin cambios de comportamiento.
func startCloudflareTunnel(port int, pluginFlag, tokenFlag string) (tunnel.State, string, error) {
	cloudflared, err := findCloudflared()
	if err != nil {
		return tunnel.State{}, "", err
	}

	token := tokenFlag
	if token == "" {
		cfg, err := tunnel.LoadConfig()
		if err != nil {
			return tunnel.State{}, "", fmt.Errorf("no pude leer la config del túnel guardada: %w", err)
		}
		token = cfg.Token
	}

	mode := "quick"
	var run *exec.Cmd
	var exposedSource string
	resolvedPort := port
	if token != "" {
		mode = "token"
		run = exec.Command(cloudflared, "tunnel", "run", "--token", token)
	} else {
		if resolvedPort == 0 {
			p, source, err := resolveTunnelPort(pluginFlag)
			if err != nil {
				return tunnel.State{}, "", err
			}
			resolvedPort = p
			exposedSource = source
		}
		run = exec.Command(cloudflared, "tunnel", "--url", fmt.Sprintf("http://localhost:%d", resolvedPort))
	}

	logPath, err := tunnel.LogPath()
	if err != nil {
		return tunnel.State{}, "", err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return tunnel.State{}, "", err
	}
	defer logFile.Close()
	run.Stdout = logFile
	run.Stderr = logFile
	localserve.SetDetached(run)

	if err := run.Start(); err != nil {
		return tunnel.State{}, "", fmt.Errorf("no pude arrancar cloudflared: %w", err)
	}
	pid := run.Process.Pid
	_ = run.Process.Release()

	publicURL := ""
	if mode == "quick" {
		publicURL = waitForQuickTunnelURL(logPath, 15*time.Second)
	}

	state := tunnel.State{
		Provider: "cloudflare", PID: pid, Port: resolvedPort, URL: publicURL,
		Mode: mode, LogPath: logPath, StartedAt: time.Now(),
	}
	return state, exposedSource, nil
}

// startNginxTunnel resuelve el puerto local (mismo criterio que
// Cloudflare) y delega el resto en internal/nginxtunnel — acá solo vive
// la parte de "qué puerto exponer", el resto es 100% del paquete.
func startNginxTunnel(port int, pluginFlag, domainFlag, emailFlag string) (tunnel.State, string, error) {
	resolvedPort := port
	var exposedSource string
	if resolvedPort == 0 {
		p, source, err := resolveTunnelPort(pluginFlag)
		if err != nil {
			return tunnel.State{}, "", err
		}
		resolvedPort, exposedSource = p, source
	}
	state, err := nginxtunnel.Start(nginxtunnel.Spec{Domain: domainFlag, Port: resolvedPort, Email: emailFlag})
	return state, exposedSource, err
}

func localTunnelStartCmd() *cobra.Command {
	var port int
	var tokenFlag string
	var pluginFlag string
	var providerFlag string
	var domainFlag string
	var emailFlag string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Levanta el túnel en segundo plano",
		Long: "--tp cloudflare (default): sin nada configurado usa un 'quick tunnel' gratis de\n" +
			"Cloudflare — genera una URL https://<random>.trycloudflare.com al vuelo, sin cuenta ni\n" +
			"dominio. Con un token guardado ('local tunnel config set --token ...', o --token acá),\n" +
			"usa ese túnel con nombre en su lugar — el que tenga tu dominio propio ya mapeado en el\n" +
			"dashboard de Cloudflare.\n\n" +
			"--tp nginx: usa un nginx ya instalado en esta máquina — necesita --domain (un dominio\n" +
			"público que ya apunte acá) y, opcionalmente, --email para activar TLS real vía certbot.\n\n" +
			"Sin --port ni --plugin explícitos (para cualquiera de los dos providers), expone el\n" +
			"plugin principal si hay uno corriendo (ver 'asterion plugin set-main'), o si no el\n" +
			"puerto de 'local serve'.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if alive, err := isAnyTunnelAlive(); err != nil {
				return err
			} else if alive {
				return fmt.Errorf("ya hay un túnel corriendo — 'asterion local tunnel stop' primero")
			}

			var state tunnel.State
			var exposedSource string
			var err error
			switch providerFlag {
			case "", "cloudflare":
				state, exposedSource, err = startCloudflareTunnel(port, pluginFlag, tokenFlag)
			case "nginx":
				state, exposedSource, err = startNginxTunnel(port, pluginFlag, domainFlag, emailFlag)
			default:
				return fmt.Errorf("--tp %q desconocido (válidos: cloudflare, nginx)", providerFlag)
			}
			if err != nil {
				return err
			}

			if err := tunnel.SaveState(state); err != nil {
				return fmt.Errorf("el túnel arrancó pero no pude guardar su estado: %w", err)
			}

			if asJSON {
				printJSON(state)
				return nil
			}

			printTunnelStarted(state, exposedSource)
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "Puerto local a exponer (default: el plugin principal, o el de 'asterion local serve')")
	cmd.Flags().StringVar(&tokenFlag, "token", "", "(--tp cloudflare) Token de un túnel con nombre — no persiste, ver 'local tunnel config set'")
	cmd.Flags().StringVar(&pluginFlag, "plugin", "", "Publicar este plugin puntual (tiene que estar corriendo) en vez del principal/local serve")
	cmd.Flags().StringVar(&providerFlag, "tp", "cloudflare", "Tunnel provider: cloudflare o nginx")
	cmd.Flags().StringVar(&domainFlag, "domain", "", "(--tp nginx) Dominio público que ya apunta a esta máquina — obligatorio con nginx")
	cmd.Flags().StringVar(&emailFlag, "email", "", "(--tp nginx) Email para registrar TLS automático real vía certbot — sin esto, HTTP plano")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el estado del túnel como JSON en vez de texto (lo usa backend-core)")
	return cmd
}

func printTunnelStarted(state tunnel.State, exposedSource string) {
	exposed := fmt.Sprintf("http://localhost:%d", state.Port)
	if exposedSource != "" {
		exposed = fmt.Sprintf("%s (%s)", exposed, exposedSource)
	}

	switch state.Provider {
	case "nginx":
		fmt.Printf("✓ Publicado con nginx — %s → %s\n", state.URL, exposed)
	default:
		if state.Mode == "token" {
			fmt.Printf("✓ Túnel (con token guardado) corriendo en segundo plano — pid %d\n", state.PID)
			fmt.Println("  la URL pública es la que configuraste como Public Hostname en el dashboard de Cloudflare")
		} else if state.URL != "" {
			fmt.Printf("✓ Túnel corriendo en segundo plano — %s → %s (pid %d)\n", state.URL, exposed, state.PID)
		} else {
			fmt.Printf("✓ Túnel arrancado (pid %d), pero todavía no pude leer la URL del log — revisala con:\n", state.PID)
			fmt.Printf("  grep trycloudflare %s\n", state.LogPath)
		}
	}
	fmt.Println("  detenerlo: asterion local tunnel stop")
}

var quickTunnelURLRe = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

// waitForQuickTunnelURL lee el log de cloudflared hasta encontrar la URL
// que imprime al arrancar un quick tunnel, con un timeout — no cuelga
// para siempre si por lo que sea tarda más: el túnel sigue corriendo en
// segundo plano igual, el usuario puede revisar el log a mano.
func waitForQuickTunnelURL(logPath string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(logPath)
		if err == nil {
			if m := quickTunnelURLRe.Find(data); m != nil {
				return string(m)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return ""
}

func localTunnelStopCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Detiene el túnel (cualquiera sea el provider que lo levantó)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, found, err := tunnel.LoadState()
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("no hay ningún túnel corriendo en segundo plano")
			}

			switch s.ResolveProvider() {
			case "nginx":
				if err := nginxtunnel.Stop(); err != nil {
					return err
				}
				if err := tunnel.RemoveState(); err != nil {
					return err
				}
			default:
				if _, err := tunnel.Stop(); err != nil {
					return err
				}
			}

			if asJSON {
				printJSON(map[string]any{"stopped": true})
				return nil
			}
			fmt.Println("✓ Túnel detenido")
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

func localTunnelStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Estado del túnel (si está corriendo, su provider/URL/puerto)",
		RunE: func(cmd *cobra.Command, args []string) error {
			initial, found, err := tunnel.LoadState()
			if err != nil {
				return err
			}

			s, alive := tunnel.State{}, false
			if found {
				if initial.ResolveProvider() == "nginx" {
					s = initial
					alive = nginxtunnel.IsAlive()
					if !alive {
						_ = tunnel.RemoveState()
					}
				} else {
					s, alive, err = tunnel.Status()
					if err != nil {
						return err
					}
				}
			}

			if asJSON {
				printJSON(map[string]any{"running": alive, "state": s})
				return nil
			}
			if !alive {
				fmt.Println("No hay ningún túnel corriendo.")
				return nil
			}
			fmt.Printf("Corriendo — provider %s", s.ResolveProvider())
			if s.PID != 0 {
				fmt.Printf(", pid %d", s.PID)
			}
			if s.Mode != "" {
				fmt.Printf(", modo %s", s.Mode)
			}
			if s.Port != 0 {
				fmt.Printf(", puerto local %d", s.Port)
			}
			fmt.Println()
			if s.Domain != "" {
				fmt.Printf("Dominio: %s\n", s.Domain)
			}
			if s.URL != "" {
				fmt.Printf("URL: %s\n", s.URL)
			}
			fmt.Printf("Arrancado: %s\n", s.StartedAt.Format(time.RFC3339))
			if s.LogPath != "" {
				fmt.Printf("Logs: %s\n", s.LogPath)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

func localTunnelConfigCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "config",
		Short: "Guardar/ver el token de un túnel con nombre de Cloudflare (dominio propio), para que 'start' lo use solo",
	}
	root.AddCommand(localTunnelConfigSetCmd(), localTunnelConfigShowCmd(), localTunnelConfigClearCmd())
	return root
}

func localTunnelConfigSetCmd() *cobra.Command {
	var token string
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Guarda (cifrado) el token de un túnel con nombre creado en el dashboard de Cloudflare",
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				return fmt.Errorf("--token es obligatorio")
			}
			if err := tunnel.SetToken(token); err != nil {
				return err
			}
			fmt.Println("✓ Token guardado — 'asterion local tunnel start' lo va a usar de acá en adelante")
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "Token del túnel (Cloudflare Zero Trust → Networks → Tunnels → tu túnel → Install a connector)")
	return cmd
}

func localTunnelConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Muestra si hay un token guardado (nunca el valor)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := tunnel.LoadConfig()
			if err != nil {
				return err
			}
			if cfg.Token == "" {
				fmt.Println("Sin token guardado — 'local tunnel start' va a usar modo quick tunnel.")
				return nil
			}
			fmt.Println("Token guardado — 'local tunnel start' va a usar tu túnel con nombre.")
			return nil
		},
	}
}

func localTunnelConfigClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Borra el token guardado (vuelve a modo quick tunnel)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := tunnel.SetToken(""); err != nil {
				return err
			}
			fmt.Println("✓ Token borrado — 'local tunnel start' vuelve a modo quick tunnel")
			return nil
		},
	}
}
