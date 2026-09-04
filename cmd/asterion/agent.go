package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/spf13/cobra"

	"asterion-core/internal/cliconfig"
	"asterion-core/internal/localserve"
	"asterion-core/internal/localstore"
	asterionruntime "asterion-core/internal/runtime"
	"asterion-core/internal/sysinfo"
	"asterion-core/internal/tunnel"
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
	root.AddCommand(agentStatusCmd(), agentRestartCmd())
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
			serviceState := "desconocido (solo Linux/systemd, macOS/launchd y Windows/Scheduled Tasks por ahora)"
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
			if runtime.GOOS == "windows" {
				serviceName = windowsTaskName(instance.ID)
				serviceState = schtasksStatus(serviceName)
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

// agentRestartCmd reinicia el proceso local del agente (systemd --user en
// Linux, launchd en macOS) SIN tocar nada del lado de Cloud — a diferencia
// de 'cloud install-agent'/'cloud disconnect'+'connect', que sirven para
// (re)vincular la instancia, esto es solo "el binario cambió, hacé que el
// servicio ya instalado corra la versión nueva". Reusa installAgentService
// tal cual (mismo código que ya usa 'cloud install-agent' para instalar el
// servicio la primera vez) — sigue siendo idempotente, reescribe el mismo
// unit/plist con el mismo contenido, y ahora sí garantiza el reinicio
// aunque ya estuviera corriendo (ver el comentario dentro de
// installAgentService sobre por qué 'restart' y no 'start').
func agentRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart [nombre-local]",
		Short: "Reinicia el servicio local del agente para que tome un binario recién actualizado",
		Long: "Sin argumento, identifica sola la instancia que representa A ESTA MÁQUINA (mismo\n" +
			"criterio que 'agent status' — ver ahí). No habla con Asterion Cloud ni cambia nada\n" +
			"de la conexión — el servicio instalado sigue corriendo el código de la última vez\n" +
			"que arrancó, Go no tiene hot-reload para un binario ya en ejecución: hace falta esto\n" +
			"después de 'asterion upgrade'/recompilar para que el agente use el binario nuevo.",
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
			if _, hasKey := keys[instance.ID]; !hasKey {
				return fmt.Errorf(
					"%q no tiene una clave de agente guardada todavía — instalalo primero con 'asterion cloud install-agent'",
					instance.Name,
				)
			}

			if err := installAgentService(instance.ID); err != nil {
				return err
			}
			fmt.Printf("✓ Agente de %q reiniciado con el binario actual\n", instance.Name)
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
// como servicio de usuario — systemd --user en Linux, launchd en macOS,
// Scheduled Task (disparada al iniciar sesión, sin pedir Administrador)
// en Windows. En el resto de los sistemas operativos el llamador debe
// mostrar el comando manual.
func installAgentService(localID string) error {
	if runtime.GOOS == "darwin" {
		return installAgentServiceDarwin(localID)
	}
	if runtime.GOOS == "windows" {
		return installAgentServiceWindows(localID)
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
	if out, err := exec.Command("systemctl", "--user", "enable", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("enable: %w (%s)", err, out)
	}
	// 'restart', no 'start'/'enable --now': si el servicio ya estaba
	// corriendo (reinstalación después de reconectar, por ejemplo), un
	// 'start' sobre un unit ya activo es un no-op — el proceso viejo
	// seguiría corriendo con la clave del agente que tenía cargada en
	// memoria desde que arrancó (loadAgentKey se llama UNA sola vez, al
	// principio de agent-run, ver agentRunCmd), sin enterarse nunca de
	// una clave nueva guardada en disco por una reconexión. 'restart'
	// arranca el servicio si no estaba corriendo, e igual de bien lo
	// reinicia si ya estaba — mismo resultado que el unload+load de
	// installAgentServiceDarwin, que sí fuerza esto en macOS.
	if out, err := exec.Command("systemctl", "--user", "restart", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("restart: %w (%s)", err, out)
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
	if runtime.GOOS == "windows" {
		return uninstallAgentServiceWindows(localID)
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

// windowsTaskName es el nombre de la Scheduled Task — mismo patrón que
// serviceName en Linux ("asterion-agent-<id>", sin el ".service").
func windowsTaskName(localID string) string {
	return "asterion-agent-" + localID
}

// escapeXMLText escapa texto dinámico (ruta del ejecutable, id local)
// antes de insertarlo en el XML de la Scheduled Task — una ruta de
// Windows con "&" (ej. un usuario "A&B") rompería el XML sin esto.
func escapeXMLText(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// writeUTF16LEFile escribe content codificado en UTF-16LE con BOM — el
// formato que usa nativamente el propio Task Scheduler de Windows (lo que
// devuelve `schtasks /query /XML`), a diferencia de JSON/YAML en el resto
// de este repo, que siempre es UTF-8 plano. Sin poder confirmarlo en una
// máquina Windows real, este es el formato documentado como el que
// `schtasks /create /XML` espera de forma confiable — no vale la pena
// arriesgar un UTF-8 que tal vez funcione.
func writeUTF16LEFile(path, content string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write([]byte{0xFF, 0xFE}); err != nil { // BOM
		return err
	}
	for _, unit := range utf16.Encode([]rune(content)) {
		if err := binary.Write(f, binary.LittleEndian, unit); err != nil {
			return err
		}
	}
	return nil
}

// installAgentServiceWindows es el equivalente Windows de
// installAgentServiceDarwin/installAgentService: una Scheduled Task
// disparada al iniciar sesión (LogonTrigger) en vez de un servicio de
// sistema — no pide Administrador, mismo espíritu que "systemd --user"/
// launchd (ver el comentario en internal/runtime/config.go sobre por qué
// nunca root). El XML de definición solo hace falta durante el propio
// `schtasks /create` (a diferencia del unit file/plist, que SON el
// servicio persistente) — se borra apenas termina.
func installAgentServiceWindows(localID string) error {
	if _, err := exec.LookPath("schtasks"); err != nil {
		return fmt.Errorf("no se encontró schtasks")
	}

	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	taskName := windowsTaskName(localID)
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
      <Arguments>agent-run --local %s</Arguments>
    </Exec>
  </Actions>
</Task>
`, escapeXMLText(exePath), escapeXMLText(localID))

	tmpFile, err := os.CreateTemp("", "asterion-agent-task-*.xml")
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

	// 'end' silencioso (por si ya corría de antes, ej. reinstalación) +
	// 'run' para forzar que arranque ahora — mismo motivo que 'restart' en
	// vez de 'start' en la versión Linux (ver el comentario en
	// installAgentService): sin esto, una instancia vieja ya en memoria
	// seguiría con la api-key vieja después de una reconexión.
	_ = exec.Command("schtasks", "/end", "/TN", taskName).Run()
	if out, err := exec.Command("schtasks", "/run", "/TN", taskName).CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks /run: %w (%s)", err, out)
	}
	return nil
}

// uninstallAgentServiceWindows es el equivalente de uninstallAgentService
// pero con schtasks. No falla si la tarea nunca se había instalado (mismo
// criterio que la versión Linux/macOS): /end y /delete sobre algo
// inexistente solo se ignoran.
func uninstallAgentServiceWindows(localID string) error {
	if _, err := exec.LookPath("schtasks"); err != nil {
		return fmt.Errorf("no se encontró schtasks")
	}
	taskName := windowsTaskName(localID)
	_ = exec.Command("schtasks", "/end", "/TN", taskName).Run()
	_ = exec.Command("schtasks", "/delete", "/TN", taskName, "/F").Run()
	return nil
}

// schtasksStatus consulta `schtasks /query /TN <name> /FO LIST /V` y
// busca la línea "Status:" — mismo criterio best-effort que
// launchdStatus. Limitación conocida y no verificable sin una máquina
// Windows real: en un Windows con idioma distinto del inglés, esa
// etiqueta puede venir traducida (ej. "Estado:" en español) — en ese caso
// esto cae a "desconocido" en vez de romper, nunca inventa un valor.
func schtasksStatus(taskName string) string {
	out, err := exec.Command("schtasks", "/query", "/TN", taskName, "/FO", "LIST", "/V").Output()
	if err != nil {
		return "no instalado"
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Status:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Status:"))
		}
	}
	return "desconocido"
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
	payload := map[string]any{"agent_version": agentVersion}

	// report_local_serve (ver internal/runtime/config.go) está apagado por
	// default — nada de esto se manda a Cloud hasta que el usuario lo
	// prenda a mano ('asterion local config set report_local_serve true').
	if cfg, err := asterionruntime.LoadConfig(); err == nil && cfg.ReportLocalServe {
		if state, running, _ := localserve.Status(); running {
			payload["local_serve_port"] = state.Port
		}
		if tstate, running, _ := tunnel.Status(); running && tstate.URL != "" {
			payload["tunnel_url"] = tstate.URL
		}
	}

	body, _ := json.Marshal(payload)
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
