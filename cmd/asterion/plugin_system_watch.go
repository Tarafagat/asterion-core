package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

// pluginSystemWatchCmd corre en primer plano, reaplicando el mismo
// archivo de sistema cada --interval — es lo que 'plugin system
// watch-install' deja instalado como servicio de fondo (ver más abajo),
// nunca pensado para uso manual directo (por eso Hidden), mismo criterio
// que agentRunCmd. Reusa applySystemFile tal cual: un tick no es más que
// "aplicar de nuevo", la misma operación idempotente que ya corre
// 'plugin system apply' a mano — si el puerto de un plugin cambió desde
// el último tick, el próximo lo detecta y reconecta/reinicia solo, sin
// que nadie tenga que acordarse de correr 'apply' de nuevo.
func pluginSystemWatchCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:    "watch <archivo.asterion>",
		Short:  "Corre en primer plano, reaplicando el archivo cada --interval (lo instala 'watch-install')",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			fmt.Printf("Vigilando %s cada %s (Ctrl+C para salir)\n", path, interval)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				if err := applySystemFile(path, false); err != nil {
					fmt.Fprintln(os.Stderr, "plugin system watch:", err)
				}
				<-ticker.C
			}
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 20*time.Second, "Cada cuánto re-resolver el wiring del sistema")
	return cmd
}

func pluginSystemWatchInstallCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "watch-install <archivo.asterion>",
		Short: "Instala un servicio de fondo que mantiene el sistema reconectado si un puerto cambia",
		Long: "Instala un proceso de fondo — systemd --user en Linux, LaunchAgent en macOS,\n" +
			"Scheduled Task en Windows, mismo mecanismo ya usado por 'asterion cloud\n" +
			"install-agent' pero un servicio HERMANO, independiente, sin depender de\n" +
			"sesión de Cloud — que corre 'plugin system watch' en loop. Si el puerto de\n" +
			"algún plugin del sistema cambia (por ejemplo, un restart que no pudo\n" +
			"recuperar su último puerto), el próximo tick lo detecta y reconecta/reinicia\n" +
			"lo que dependía de él, sin que nadie tenga que correr 'apply' a mano.\n\n" +
			"Sondeo, no push instantáneo — el intervalo (20s por default) es el margen\n" +
			"real, no 'al instante'.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			if _, err := os.Stat(abs); err != nil {
				return fmt.Errorf("no encontré %s: %w", abs, err)
			}
			if err := installSystemWatchService(abs, interval); err != nil {
				return err
			}
			fmt.Printf("✓ servicio de fondo instalado — vigila %s cada %s\n", abs, interval)
			return nil
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 20*time.Second, "Cada cuánto re-resolver el wiring del sistema")
	return cmd
}

func pluginSystemWatchUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch-uninstall <archivo.asterion>",
		Short: "Quita el servicio de fondo instalado por 'watch-install' para este archivo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			if err := uninstallSystemWatchService(abs); err != nil {
				return err
			}
			fmt.Printf("✓ servicio de fondo para %s desinstalado\n", abs)
			return nil
		},
	}
}

// systemWatchID es un identificador corto y estable derivado del path
// absoluto del .asterion — hace falta porque el label/nombre de unit/
// nombre de task no puede ser el path tal cual (barras, espacios,
// caracteres no válidos en cada uno de los 3 formatos) y porque puede
// haber más de un sistema vigilado a la vez sin pisarse entre sí.
func systemWatchID(absPath string) string {
	sum := sha256.Sum256([]byte(absPath))
	return hex.EncodeToString(sum[:])[:12]
}

func installSystemWatchService(absPath string, interval time.Duration) error {
	switch runtime.GOOS {
	case "darwin":
		return installSystemWatchServiceDarwin(absPath, interval)
	case "windows":
		return installSystemWatchServiceWindows(absPath, interval)
	case "linux":
		return installSystemWatchServiceLinux(absPath, interval)
	default:
		return fmt.Errorf("la instalación automática del servicio de fondo todavía solo soporta Linux (systemd --user), macOS (launchd) y Windows (Scheduled Task)")
	}
}

func uninstallSystemWatchService(absPath string) error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallSystemWatchServiceDarwin(absPath)
	case "windows":
		return uninstallSystemWatchServiceWindows(absPath)
	case "linux":
		return uninstallSystemWatchServiceLinux(absPath)
	default:
		return fmt.Errorf("la desinstalación automática del servicio de fondo todavía solo soporta Linux (systemd --user), macOS (launchd) y Windows (Scheduled Task)")
	}
}

// --- Linux (systemd --user) ------------------------------------------------
//
// Mismo patrón exacto que installAgentService/uninstallAgentService (ver
// agent.go) — la única diferencia real es el nombre de la unit y el
// comando que arranca (plugin system watch, no agent-run).

func systemWatchUnitName(absPath string) string {
	return "asterion-system-watch-" + systemWatchID(absPath) + ".service"
}

func installSystemWatchServiceLinux(absPath string, interval time.Duration) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("no se encontró systemctl")
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}

	unit := fmt.Sprintf(`[Unit]
Description=Asterion — vigila el sistema de plugins declarado en %s
After=network-online.target

[Service]
ExecStart=%s plugin system watch %s --interval %s
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
`, absPath, exePath, absPath, interval)

	serviceName := systemWatchUnitName(absPath)
	unitPath := filepath.Join(unitDir, serviceName)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload: %w (%s)", err, out)
	}
	if out, err := exec.Command("systemctl", "--user", "enable", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("enable: %w (%s)", err, out)
	}
	if out, err := exec.Command("systemctl", "--user", "restart", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("restart: %w (%s)", err, out)
	}
	return nil
}

func uninstallSystemWatchServiceLinux(absPath string) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("no se encontró systemctl")
	}
	serviceName := systemWatchUnitName(absPath)
	_ = exec.Command("systemctl", "--user", "disable", "--now", serviceName).Run()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", serviceName)
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

// --- macOS (launchd) --------------------------------------------------------

func systemWatchLabel(absPath string) string {
	return "com.asterion.system-watch." + systemWatchID(absPath)
}

func systemWatchPlistPath(absPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", systemWatchLabel(absPath)+".plist"), nil
}

func installSystemWatchServiceDarwin(absPath string, interval time.Duration) error {
	if _, err := exec.LookPath("launchctl"); err != nil {
		return fmt.Errorf("no se encontró launchctl")
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	logPath := filepath.Join(home, "Library", "Logs", "asterion-system-watch-"+systemWatchID(absPath)+".log")

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>plugin</string>
		<string>system</string>
		<string>watch</string>
		<string>%s</string>
		<string>--interval</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, systemWatchLabel(absPath), exePath, absPath, interval, logPath, logPath)

	plistPath, err := systemWatchPlistPath(absPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}

	_ = exec.Command("launchctl", "unload", "-w", plistPath).Run()
	if out, err := exec.Command("launchctl", "load", "-w", plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %w (%s)", err, out)
	}
	return nil
}

func uninstallSystemWatchServiceDarwin(absPath string) error {
	if _, err := exec.LookPath("launchctl"); err != nil {
		return fmt.Errorf("no se encontró launchctl")
	}
	plistPath, err := systemWatchPlistPath(absPath)
	if err != nil {
		return err
	}
	_ = exec.Command("launchctl", "unload", "-w", plistPath).Run()
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// --- Windows (Scheduled Task) -----------------------------------------------

func systemWatchTaskName(absPath string) string {
	return "asterion-system-watch-" + systemWatchID(absPath)
}

func installSystemWatchServiceWindows(absPath string, interval time.Duration) error {
	if _, err := exec.LookPath("schtasks"); err != nil {
		return fmt.Errorf("no se encontró schtasks")
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	taskName := systemWatchTaskName(absPath)
	args := fmt.Sprintf("plugin system watch %s --interval %s", absPath, interval)
	xmlDef := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
    </LogonTrigger>
  </Triggers>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, escapeXMLText(exePath), escapeXMLText(args))

	tmpFile, err := os.CreateTemp("", "asterion-system-watch-task-*.xml")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(tmpPath)
	if err := writeUTF16LEFile(tmpPath, xmlDef); err != nil {
		return err
	}

	if out, err := exec.Command("schtasks", "/create", "/TN", taskName, "/XML", tmpPath, "/F").CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /create: %w (%s)", err, out)
	}
	_ = exec.Command("schtasks", "/end", "/TN", taskName).Run()
	if out, err := exec.Command("schtasks", "/run", "/TN", taskName).CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /run: %w (%s)", err, out)
	}
	return nil
}

func uninstallSystemWatchServiceWindows(absPath string) error {
	if _, err := exec.LookPath("schtasks"); err != nil {
		return fmt.Errorf("no se encontró schtasks")
	}
	taskName := systemWatchTaskName(absPath)
	_ = exec.Command("schtasks", "/end", "/TN", taskName).Run()
	_ = exec.Command("schtasks", "/delete", "/TN", taskName, "/F").Run()
	return nil
}
