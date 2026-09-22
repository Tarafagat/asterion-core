//go:build windows

package sysservices

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Control ejecuta start/stop/restart sobre UN servicio puntual — mismo
// contrato que Linux/macOS (exec.Command con args separados, nunca shell,
// revalida acción/nombre/denylist ACÁ ADENTRO sin importar qué haya
// validado ya el caller). unitName ya pasó ValidUnitName antes de
// interpolarse en el string de PowerShell (solo alfanumérico/punto/
// guion/guion bajo/'+' — sin comillas ni '$' ni backtick posibles), así
// que no hay forma de inyectar código PowerShell por acá.
//
// Sin sudo: a diferencia de Linux, depende de que el proceso llamante YA
// corra elevado (SYSTEM/Administrator) — si no, Start-Service/
// Stop-Service fallan con un error de PowerShell claro ("Access is
// denied") en vez de disparar un prompt de UAC (nunca interactivo, mismo
// criterio que `sudo -n` en Linux).
//
// -Force en Stop-Service/Restart-Service (Start-Service no tiene ese
// parámetro — no hay nada que "forzar" al arrancar) para que también
// pare los servicios que dependen de este, mismo comportamiento por
// defecto que `systemctl stop` (que también para dependientes).
func Control(action, unitName string) (Unit, error) {
	if !ValidAction(action) {
		return Unit{}, fmt.Errorf("acción desconocida: %q (válidas: start, stop, restart)", action)
	}
	if !ValidUnitName(unitName) {
		return Unit{}, fmt.Errorf("nombre de servicio inválido: %q", unitName)
	}
	if IsProtected(unitName) {
		return Unit{}, fmt.Errorf("%q está en la lista de servicios protegidos — Asterion nunca lo reinicia/para/inicia de forma remota, pase lo que pida Cloud", unitName)
	}

	var psCommand string
	switch action {
	case "start":
		psCommand = fmt.Sprintf(`Start-Service -Name '%s' -ErrorAction Stop`, unitName)
	case "stop":
		psCommand = fmt.Sprintf(`Stop-Service -Name '%s' -Force -ErrorAction Stop`, unitName)
	case "restart":
		psCommand = fmt.Sprintf(`Restart-Service -Name '%s' -Force -ErrorAction Stop`, unitName)
	}
	if out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCommand).CombinedOutput(); err != nil {
		return Unit{}, fmt.Errorf(
			"%s -Name %s falló: %v (%s) — ¿corre este proceso elevado (SYSTEM/Administrator)?",
			action, unitName, err, strings.TrimSpace(string(out)),
		)
	}

	// "Verificar": re-consulta el estado real de ESTE servicio después de
	// actuar, en vez de asumir que exit code 0 == quedó como se esperaba.
	return status(unitName)
}

// status consulta un solo servicio — sin privilegios, para el paso
// "verificar" de Control() y para Get(). Prefiere PowerShell
// (estructurado); si PowerShell no está disponible o devuelve algo
// inesperado, cae a `sc query <nombre>` (mismo criterio "preferir
// estructurado, caer a texto plano" que List()). Un nombre bien formado
// pero inexistente no es un error en ninguno de los dos caminos — se
// traduce a LoadState "not-found", mismo criterio que Linux/macOS.
func status(unitName string) (Unit, error) {
	psCommand := fmt.Sprintf(
		`Get-Service -Name '%s' -ErrorAction Stop | Select-Object Name, DisplayName, @{N='Status';E={$_.Status.ToString()}} | ConvertTo-Json -Compress`,
		unitName,
	)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCommand).CombinedOutput()
	if err == nil {
		var r serviceInfo
		if jsonErr := json.Unmarshal(out, &r); jsonErr == nil {
			return Unit{
				Name: r.Name, LoadState: "loaded",
				ActiveState: activeStateFromStatusWord(r.Status), SubState: r.Status,
				Description: r.DisplayName,
				Protected:   IsProtected(unitName), Category: Classify(unitName),
			}, nil
		}
		// PowerShell corrió pero el JSON no vino como se esperaba: cae a
		// sc.exe en vez de fallar de punta a punta.
	} else if bytes.Contains(out, []byte("Cannot find")) {
		return notFoundUnit(unitName), nil
	}

	scOut, scErr := exec.Command("sc", "query", unitName).CombinedOutput()
	if scErr != nil {
		if bytes.Contains(scOut, []byte("does not exist")) {
			return notFoundUnit(unitName), nil
		}
		return Unit{}, fmt.Errorf("no se pudo verificar el estado de %q después de actuar: %w", unitName, scErr)
	}
	units := parseScQuery(scOut)
	if len(units) == 0 {
		return Unit{}, fmt.Errorf("no se pudo interpretar la salida de sc.exe para %q", unitName)
	}
	u := units[0]
	u.Protected = IsProtected(unitName)
	u.Category = Classify(unitName)
	return u, nil
}

func notFoundUnit(unitName string) Unit {
	return Unit{
		Name: unitName, LoadState: "not-found", ActiveState: "inactive",
		Protected: IsProtected(unitName), Category: Classify(unitName),
	}
}
