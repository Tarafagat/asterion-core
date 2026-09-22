//go:build darwin

package sysservices

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Control ejecuta start/stop/restart sobre UN label puntual — mismo
// contrato que la versión de Linux (exec.Command con args separados,
// nunca shell, revalida acción/nombre/denylist ACÁ ADENTRO sin importar
// qué haya validado ya el caller). Usa isDarwinProtected (no solo
// IsProtected) para que el namespace "com.apple." completo quede
// bloqueado, no solo lo que ya está en la denylist compartida.
//
// Sin sudo: a diferencia de Linux (que necesita una regla sudoers
// puntual), acá depende de que el proceso llamante YA corra como root —
// si no, launchctl falla al toque con un error claro (mismo espíritu que
// `sudo -n`: nunca se intenta un prompt interactivo).
//
// "stop" puede no pegar: si el LaunchDaemon tiene KeepAlive configurado,
// launchd puede reiniciarlo solo apenas lo para — comportamiento real de
// la plataforma, no un bug de este código. Por eso Control() siempre
// re-consulta el estado real después de actuar (igual que Linux) y
// devuelve lo que efectivamente observa, nunca lo que "debería" haber
// pasado.
func Control(action, unitName string) (Unit, error) {
	if !ValidAction(action) {
		return Unit{}, fmt.Errorf("acción desconocida: %q (válidas: start, stop, restart)", action)
	}
	if !ValidUnitName(unitName) {
		return Unit{}, fmt.Errorf("nombre de servicio inválido: %q", unitName)
	}
	if isDarwinProtected(unitName) {
		return Unit{}, fmt.Errorf("%q está en la lista de servicios protegidos — Asterion nunca lo reinicia/para/inicia de forma remota, pase lo que pida Cloud", unitName)
	}

	var cmd *exec.Cmd
	switch action {
	case "restart":
		// kickstart -k: si ya está corriendo, lo mata y lo vuelve a
		// arrancar en el momento — el equivalente real más cercano a
		// "restart" que tiene launchctl (no existe un verbo "restart").
		cmd = exec.Command("launchctl", "kickstart", "-k", "system/"+unitName)
	case "start":
		cmd = exec.Command("launchctl", "start", unitName)
	case "stop":
		cmd = exec.Command("launchctl", "stop", unitName)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return Unit{}, fmt.Errorf("launchctl %s %s falló: %v (%s) — ¿corre este proceso como root?", action, unitName, err, strings.TrimSpace(string(out)))
	}

	// "Verificar": re-consulta el estado real de ESTE label después de
	// actuar, en vez de asumir que exit code 0 == quedó como se esperaba.
	return status(unitName)
}

// status consulta un solo label vía `launchctl print system/<label>` —
// sin privilegios, para el paso "verificar" de Control() y para Get().
// Un label bien formado pero no cargado no es un error (exit 113,
// "Could not find service ... in domain for system", confirmado en
// vivo) — se traduce a LoadState "not-found", mismo criterio que Linux
// ante una unidad systemd inexistente.
func status(unitName string) (Unit, error) {
	out, err := exec.Command("launchctl", "print", "system/"+unitName).CombinedOutput()
	if err != nil {
		if bytes.Contains(out, []byte("Could not find service")) {
			return Unit{
				Name: unitName, LoadState: "not-found", ActiveState: "inactive",
				Protected: isDarwinProtected(unitName), Category: Classify(unitName),
			}, nil
		}
		return Unit{}, fmt.Errorf("no se pudo verificar el estado de %q después de actuar: %w", unitName, err)
	}
	return Unit{
		Name: unitName, LoadState: "loaded",
		ActiveState: activeStateFromPrint(out),
		Description: programFromPrint(out),
		Protected:   isDarwinProtected(unitName), Category: Classify(unitName),
	}, nil
}

// activeStateFromPrint busca la línea "state = ..." del bloque que
// imprime `launchctl print` — visto en vivo: "running" y "not running"
// son los dos valores más comunes; cualquier otra cosa (ej. "waiting",
// para jobs on-demand) cae a "inactive" en vez de intentar enumerar cada
// posible valor que Apple no documenta.
func activeStateFromPrint(out []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if rest, ok := strings.CutPrefix(line, "state = "); ok {
			if rest == "running" {
				return "active"
			}
			return "inactive"
		}
	}
	return "inactive"
}

// programFromPrint busca la línea "program = ..." como el sustituto más
// cercano a una descripción — launchd no tiene un campo "Description"
// como systemd, así que esto es lo más informativo que hay disponible sin
// inventar nada.
func programFromPrint(out []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if rest, ok := strings.CutPrefix(line, "program = "); ok {
			return rest
		}
	}
	return ""
}
