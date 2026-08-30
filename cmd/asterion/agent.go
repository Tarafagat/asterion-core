package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"asterion-core/internal/cliconfig"
	"asterion-core/internal/localstore"
	"asterion-core/internal/sysinfo"
)

// agentCmd agrupa el estado LOCAL del servicio del agente — distinto de
// `asterion cloud install-agent`/`uninstall-agent`, que además hablan con
// Cloud. Esto solo mira lo que hay en esta máquina (clave guardada,
// systemd), sin red.
func agentCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "agent",
		Short: "Estado local del agente instalado (systemd, clave guardada)",
	}
	root.AddCommand(agentStatusCmd())
	return root
}

func agentStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [nombre-local]",
		Short: "Muestra si el agente de esta máquina (o, con un nombre, de esa instancia local) está instalado y corriendo",
		Long: "Sin argumento, identifica sola la instancia que representa A ESTA MÁQUINA — la que\n" +
			"'asterion cloud install-agent' registró acá (Host=localhost es la marca real, no el\n" +
			"nombre: funciona aunque se haya usado --name para elegir un nombre distinto del\n" +
			"hostname). Pasá un nombre explícito para consultar cualquier otra instancia de tu\n" +
			"inventario local.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var instance localstore.Instance
			var err error
			if len(args) == 1 {
				instance, err = localstore.Get(args[0])
			} else {
				instance, err = resolveSelfInstance()
			}
			if err != nil {
				return err
			}

			keys, err := loadAgentKeys()
			if err != nil {
				return err
			}
			_, hasKey := keys[instance.ID]

			serviceName := "asterion-agent-" + instance.ID + ".service"
			serviceState := "desconocido (solo Linux/systemd y macOS/launchd por ahora)"
			if runtime.GOOS == "linux" {
				if _, err := exec.LookPath("systemctl"); err == nil {
					out, _ := exec.Command("systemctl", "--user", "is-active", serviceName).Output()
					serviceState = strings.TrimSpace(string(out))
					if serviceState == "" {
						serviceState = "inactive"
					}
				}
			}
			if runtime.GOOS == "darwin" {
				serviceName = launchdLabel(instance.ID)
				serviceState = launchdStatus(serviceName)
			}

			printJSON(map[string]any{
				"instance":       instance.Name,
				"local_id":       instance.ID,
				"has_agent_key":  hasKey,
				"service_name":   serviceName,
				"service_status": serviceState,
			})
			return nil
		},
	}
}

// resolveSelfInstance encuentra, en el inventario local, el perfil que
// representa A ESTA MÁQUINA — el que 'cloud install-agent' registra
// siempre con Host="localhost" (ver cloud.go:cloudInstallAgentCmd). Es el
// marcador real, no el nombre: install-agent arma el nombre por default a
// partir del hostname ("self-" + hostname), pero --name puede pisarlo, así
// que adivinar por nombre sería frágil. Si hay más de un perfil marcado
// así (alguien agregó "localhost" a mano con 'instances add', por
// ejemplo), no adivina cuál — lista los nombres y pide uno explícito.
func resolveSelfInstance() (localstore.Instance, error) {
	list, err := localstore.List()
	if err != nil {
		return localstore.Instance{}, err
	}

	var self []localstore.Instance
	for _, inst := range list {
		if inst.Host == "localhost" {
			self = append(self, inst)
		}
	}

	switch len(self) {
	case 0:
		return localstore.Instance{}, fmt.Errorf(
			"esta máquina no tiene ningún agente instalado — corré 'asterion cloud install-agent' primero, " +
				"o pasá el nombre de una instancia local: 'asterion agent status <nombre>'",
		)
	case 1:
		return self[0], nil
	default:
		names := make([]string, len(self))
		for i, inst := range self {
			names[i] = inst.Name
		}
		return localstore.Instance{}, fmt.Errorf(
			"hay %d instancias locales que podrían ser esta máquina (%s) — pasá el nombre explícito: 'asterion agent status <nombre>'",
			len(self), strings.Join(names, ", "),
		)
	}
}

func agentKeysPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "asterion")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-keys.json"), nil
}

func loadAgentKeys() (map[string]string, error) {
	path, err := agentKeysPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	keys := map[string]string{}
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, err
	}
	return keys, nil
}

func saveAgentKey(localID, rawKey string) error {
	keys, err := loadAgentKeys()
	if err != nil {
		return err
	}
	keys[localID] = rawKey
	path, err := agentKeysPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func loadAgentKey(localID string) (string, error) {
	keys, err := loadAgentKeys()
	if err != nil {
		return "", err
	}
	key, ok := keys[localID]
	if !ok {
		return "", fmt.Errorf("no hay una clave de agente guardada para %q — corré 'asterion cloud connect' o 'asterion cloud install-agent' primero", localID)
	}
	return key, nil
}

// removeAgentKey borra la clave local guardada para localID, si existe.
// No es un error que ya no esté — uninstall-agent puede correrse dos veces.
func removeAgentKey(localID string) {
	keys, err := loadAgentKeys()
	if err != nil {
		return
	}
	if _, ok := keys[localID]; !ok {
		return
	}
	delete(keys, localID)
	path, err := agentKeysPath()
	if err != nil {
		return
	}
	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// installAgentService deja `asterion agent-run --local <id>` corriendo
// como servicio de usuario — systemd --user en Linux, launchd en macOS. En
// el resto de los sistemas operativos el llamador debe mostrar el comando
// manual.
func installAgentService(localID string) error {
	if runtime.GOOS == "darwin" {
		return installAgentServiceDarwin(localID)
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("la instalación automática del servicio todavía solo soporta Linux (systemd --user) y macOS (launchd)")
	}
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
Description=Asterion agent (%s)
After=network-online.target

[Service]
ExecStart=%s agent-run --local %s
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
`, localID, exePath, localID)

	unitPath := filepath.Join(unitDir, "asterion-agent-"+localID+".service")
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}

	serviceName := filepath.Base(unitPath)
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload: %w (%s)", err, out)
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("enable --now: %w (%s)", err, out)
	}
	return nil
}

// uninstallAgentService revierte installAgentService: para el servicio,
// lo deshabilita y borra su unit file. No falla si nunca se había
// instalado (systemctl stop/disable sobre algo inexistente no es fatal
// acá, solo se ignora el resultado).
func uninstallAgentService(localID string) error {
	if runtime.GOOS == "darwin" {
		return uninstallAgentServiceDarwin(localID)
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("la desinstalación automática del servicio todavía solo soporta Linux (systemd --user) y macOS (launchd)")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("no se encontró systemctl")
	}

	serviceName := "asterion-agent-" + localID + ".service"
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

// launchdLabel es el identificador reverse-DNS que usa launchd para un
// agente de usuario — el mismo rol que el nombre de unit de systemd.
func launchdLabel(localID string) string {
	return "com.asterion.agent." + localID
}

func launchdPlistPath(localID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel(localID)+".plist"), nil
}

// installAgentServiceDarwin es el equivalente de installAgentService pero
// con launchd: un plist de usuario en ~/Library/LaunchAgents (nunca un
// LaunchDaemon de sistema — ver mismo criterio que systemd --user, no root)
// con RunAtLoad+KeepAlive para que se levante solo al iniciar sesión y se
// reinicie si el proceso muere, igual que Restart=always en la unit de
// systemd.
func installAgentServiceDarwin(localID string) error {
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
	logPath := filepath.Join(home, "Library", "Logs", "asterion-agent-"+localID+".log")

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>agent-run</string>
		<string>--local</string>
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
`, launchdLabel(localID), exePath, localID, logPath, logPath)

	plistPath, err := launchdPlistPath(localID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}

	// unload primero (silencioso, por si ya había un plist viejo cargado)
	// para que load no falle con "already loaded" en una reinstalación.
	_ = exec.Command("launchctl", "unload", "-w", plistPath).Run()
	if out, err := exec.Command("launchctl", "load", "-w", plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %w (%s)", err, out)
	}
	return nil
}

// uninstallAgentServiceDarwin es el equivalente de uninstallAgentService
// pero con launchd.
func uninstallAgentServiceDarwin(localID string) error {
	if _, err := exec.LookPath("launchctl"); err != nil {
		return fmt.Errorf("no se encontró launchctl")
	}
	plistPath, err := launchdPlistPath(localID)
	if err != nil {
		return err
	}
	_ = exec.Command("launchctl", "unload", "-w", plistPath).Run()
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// launchdStatus consulta `launchctl list <label>` — si el label no está
// cargado, launchctl devuelve error (servicio no instalado); si está
// cargado pero el proceso no corre ahora, el campo "PID" del plist que
// imprime es "-" en vez de un número.
func launchdStatus(label string) string {
	out, err := exec.Command("launchctl", "list", label).Output()
	if err != nil {
		return "no instalado"
	}
	if strings.Contains(string(out), `"PID" = `) {
		return "active"
	}
	return "loaded (no corriendo ahora)"
}

// agentVersion se manda en cada heartbeat — Asterion Cloud lo muestra en el
// panel del agente y lo compara contra versiones disponibles (spec §49).
// Sin un mecanismo de release todavía, queda fijo acá a mano por ahora.
const agentVersion = "0.1.0"

func agentRunCmd() *cobra.Command {
	var localID string
	var interval time.Duration
	var heartbeatInterval time.Duration

	cmd := &cobra.Command{
		Use:    "agent-run",
		Short:  "Corre en primer plano reportando métricas y heartbeat de esta máquina (lo instala 'asterion cloud install-agent')",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return err
			}
			apiKey, err := loadAgentKey(localID)
			if err != nil {
				return err
			}

			fmt.Printf("Reportando métricas de %q a %s cada %s (heartbeat cada %s)\n", localID, cfg.APIBaseURL, interval, heartbeatInterval)

			// Heartbeat y métricas son dos ciclos independientes (spec §24):
			// el heartbeat es más frecuente y liviano, así Cloud sabe "sigo
			// vivo" sin depender de que en ese momento haya métricas nuevas.
			go func() {
				ticker := time.NewTicker(heartbeatInterval)
				defer ticker.Stop()
				for {
					if err := reportHeartbeat(cfg.APIBaseURL, apiKey); err != nil {
						fmt.Fprintln(os.Stderr, "agente (heartbeat):", err)
					}
					<-ticker.C
				}
			}()

			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			// Primer reporte inmediato, después uno por tick.
			for {
				if err := reportOnce(cfg.APIBaseURL, apiKey); err != nil {
					fmt.Fprintln(os.Stderr, "agente:", err)
				}
				<-ticker.C
			}
		},
	}
	cmd.Flags().StringVar(&localID, "local", "", "Id local de la instancia (inst_xxxxxxxx)")
	cmd.Flags().DurationVar(&interval, "interval", 60*time.Second, "Cada cuánto reportar métricas")
	cmd.Flags().DurationVar(&heartbeatInterval, "heartbeat-interval", 30*time.Second, "Cada cuánto reportar heartbeat")
	_ = cmd.MarkFlagRequired("local")
	return cmd
}

// reportHeartbeat le dice a Cloud "sigo vivo", separado de las métricas —
// ver POST /agent/heartbeat. Cloud calcula ONLINE/OFFLINE/STALE a partir
// de cuándo llegó el último de estos, no de las métricas.
func reportHeartbeat(apiBaseURL, apiKey string) error {
	body, _ := json.Marshal(map[string]any{"agent_version": agentVersion})
	req, err := http.NewRequest(http.MethodPost, apiBaseURL+"/agent/heartbeat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Asterion-Api-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("la API respondió %d al reportar heartbeat", resp.StatusCode)
	}
	return nil
}

// reportOnce manda datos crudos, nunca un costo: cuánto sale eso lo
// calcula el pricing engine de Asterion Cloud con la tarifa vigente del
// proyecto, no el agente.
func reportOnce(apiBaseURL, apiKey string) error {
	snap, err := sysinfo.Collect()
	if err != nil {
		return err
	}

	type metricPoint struct {
		MetricType string  `json:"metric_type"`
		Value      float64 `json:"value"`
	}
	metrics := []metricPoint{
		{"cpu_percent", snap.CPUPercent},
		{"ram_used_gb", snap.RAMUsedGB},
		{"disk_used_gb", snap.DiskUsedGB},
		{"network_in_gb", snap.NetworkInGB},
		{"network_out_gb", snap.NetworkOutGB},
	}

	body, _ := json.Marshal(map[string]any{"metrics": metrics})
	req, err := http.NewRequest(http.MethodPost, apiBaseURL+"/agent/usage-metrics", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Asterion-Api-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("la API respondió %d al reportar métricas", resp.StatusCode)
	}
	return nil
}
