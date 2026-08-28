package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"asterion-core/internal/localserve"
	"asterion-core/internal/tunnel"
)

// localTunnelCmd expone Cloudflare Tunnel como subcomando del CLI — para
// no tener que instalar/configurar cloudflared a mano cada vez que hace
// falta una URL pública para lo que sirve `asterion local serve` (u otro
// puerto cualquiera). Dos modos:
//
//   - "quick" (default, sin nada configurado): cloudflared genera una URL
//     https://<random>.trycloudflare.com al vuelo — sin cuenta de
//     Cloudflare, sin dominio propio, gratis. Es lo que se usa mientras no
//     hay un dominio disponible.
//   - "token" (con `local tunnel config set --token ...`): usa un túnel ya
//     creado a mano en el dashboard de Cloudflare (Networks → Tunnels),
//     con su propio hostname público ya mapeado ahí a un puerto local —
//     es lo que da una URL con dominio propio, estable entre reinicios.
func localTunnelCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "tunnel",
		Short: "Expone un puerto local con una URL pública HTTPS, vía Cloudflare Tunnel",
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

func localTunnelStartCmd() *cobra.Command {
	var port int
	var tokenFlag string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Levanta el túnel en segundo plano",
		Long: "Sin nada configurado, usa un 'quick tunnel' gratis de Cloudflare — genera una URL\n" +
			"https://<random>.trycloudflare.com al vuelo, sin cuenta ni dominio. Si guardaste un token\n" +
			"con 'local tunnel config set --token ...' (o lo pasás acá con --token), usa ese túnel con\n" +
			"nombre en su lugar — el que tenga tu dominio propio ya mapeado en el dashboard.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, alive, _ := tunnel.Status(); alive {
				return fmt.Errorf("ya hay un túnel corriendo — 'asterion local tunnel stop' primero")
			}

			cloudflared, err := findCloudflared()
			if err != nil {
				return err
			}

			token := tokenFlag
			if token == "" {
				cfg, err := tunnel.LoadConfig()
				if err != nil {
					return fmt.Errorf("no pude leer la config del túnel guardada: %w", err)
				}
				token = cfg.Token
			}

			mode := "quick"
			var run *exec.Cmd
			if token != "" {
				mode = "token"
				run = exec.Command(cloudflared, "tunnel", "run", "--token", token)
			} else {
				resolvedPort := port
				if resolvedPort == 0 {
					s, alive, err := localserve.Status()
					if err != nil {
						return err
					}
					if !alive {
						return fmt.Errorf("no hay ningún 'asterion local serve' corriendo y no pasaste --port — decime qué puerto exponer")
					}
					resolvedPort = s.Port
				}
				port = resolvedPort
				run = exec.Command(cloudflared, "tunnel", "--url", fmt.Sprintf("http://localhost:%d", resolvedPort))
			}

			logPath, err := tunnel.LogPath()
			if err != nil {
				return err
			}
			logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			defer logFile.Close()
			run.Stdout = logFile
			run.Stderr = logFile
			localserve.SetDetached(run)

			if err := run.Start(); err != nil {
				return fmt.Errorf("no pude arrancar cloudflared: %w", err)
			}
			pid := run.Process.Pid
			_ = run.Process.Release()

			publicURL := ""
			if mode == "quick" {
				publicURL = waitForQuickTunnelURL(logPath, 15*time.Second)
			}

			if err := tunnel.SaveState(tunnel.State{
				PID: pid, Port: port, URL: publicURL, Mode: mode, LogPath: logPath, StartedAt: time.Now(),
			}); err != nil {
				return fmt.Errorf("el túnel arrancó (pid %d) pero no pude guardar su estado: %w", pid, err)
			}

			if mode == "token" {
				fmt.Printf("✓ Túnel (con token guardado) corriendo en segundo plano — pid %d\n", pid)
				fmt.Println("  la URL pública es la que configuraste como Public Hostname en el dashboard de Cloudflare")
			} else if publicURL != "" {
				fmt.Printf("✓ Túnel corriendo en segundo plano — %s → http://localhost:%d (pid %d)\n", publicURL, port, pid)
			} else {
				fmt.Printf("✓ Túnel arrancado (pid %d), pero todavía no pude leer la URL del log — revisala con:\n", pid)
				fmt.Printf("  grep trycloudflare %s\n", logPath)
			}
			fmt.Printf("  logs: %s\n", logPath)
			fmt.Println("  detenerlo: asterion local tunnel stop")
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "Puerto local a exponer (default: el de 'asterion local serve' si está corriendo)")
	cmd.Flags().StringVar(&tokenFlag, "token", "", "Token de un túnel con nombre de Cloudflare — no persiste, ver 'local tunnel config set' para guardarlo")
	return cmd
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
	return &cobra.Command{
		Use:   "stop",
		Short: "Detiene el túnel",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := tunnel.Stop(); err != nil {
				return err
			}
			fmt.Println("✓ Túnel detenido")
			return nil
		},
	}
}

func localTunnelStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Estado del túnel (si está corriendo, su URL/puerto/pid)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, alive, err := tunnel.Status()
			if err != nil {
				return err
			}
			if !alive {
				fmt.Println("No hay ningún túnel corriendo.")
				return nil
			}
			fmt.Printf("Corriendo — pid %d, modo %s", s.PID, s.Mode)
			if s.Port != 0 {
				fmt.Printf(", puerto local %d", s.Port)
			}
			fmt.Println()
			if s.URL != "" {
				fmt.Printf("URL: %s\n", s.URL)
			}
			fmt.Printf("Arrancado: %s\n", s.StartedAt.Format(time.RFC3339))
			fmt.Printf("Logs: %s\n", s.LogPath)
			return nil
		},
	}
}

func localTunnelConfigCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "config",
		Short: "Guardar/ver el token de un túnel con nombre (dominio propio), para que 'start' lo use solo",
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
