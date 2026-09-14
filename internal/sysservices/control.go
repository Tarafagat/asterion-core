package sysservices

import (
	"fmt"
	"os/exec"
	"strings"
)

// Control ejecuta start/stop/restart sobre UNA unidad puntual — el único
// camino de mutación de todo este paquete. exec.Command recibe cada
// argumento por separado (nunca /bin/sh -c ni concatenación de string), así
// que no hay forma de inyectar shell. Revalida acción/nombre/denylist ACÁ
// ADENTRO sin importar qué haya validado ya el caller (Python en el borde,
// agentjobs.go antes de llamar) — cada capa desconfía de la anterior, mismo
// criterio que el resto de Asterion.
func Control(action, unitName string) (Unit, error) {
	if !ValidAction(action) {
		return Unit{}, fmt.Errorf("acción desconocida: %q (válidas: start, stop, restart)", action)
	}
	if !ValidUnitName(unitName) {
		return Unit{}, fmt.Errorf("nombre de unidad inválido: %q", unitName)
	}
	if IsProtected(unitName) {
		return Unit{}, fmt.Errorf("%q está en la lista de unidades protegidas — Asterion nunca la reinicia/para/inicia de forma remota, pase lo que pida Cloud", unitName)
	}

	// -n/--non-interactive: si la regla de sudo no matchea (nunca se corrió
	// 'agent enable-service-control', o el usuario cambió), esto falla al
	// toque con un error claro en vez de colgarse esperando una contraseña
	// que jamás va a llegar (este proceso no tiene terminal).
	if out, err := exec.Command("sudo", "-n", "systemctl", action, unitName).CombinedOutput(); err != nil {
		return Unit{}, fmt.Errorf(
			"systemctl %s %s falló: %v (%s) — ¿corriste 'sudo asterion agent enable-service-control <usuario>'?",
			action, unitName, err, strings.TrimSpace(string(out)),
		)
	}

	// "Verificar": re-consulta el estado real de ESTA unidad después de
	// actuar, en vez de asumir que exit code 0 == quedó como se esperaba
	// (mismo espíritu que osuser — nunca confiar solo en que Apply no
	// devolvió error).
	return status(unitName)
}

// status consulta una sola unidad — sin sudo, leer estado nunca lo
// necesita — para el paso "verificar" de Control.
func status(unitName string) (Unit, error) {
	out, err := exec.Command(
		"systemctl", "show", unitName,
		"--property=LoadState,ActiveState,SubState,Description", "--value",
	).Output()
	if err != nil {
		return Unit{}, fmt.Errorf("no se pudo verificar el estado de %q después de actuar: %w", unitName, err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	for len(lines) < 4 {
		lines = append(lines, "")
	}
	return Unit{
		Name: unitName, LoadState: lines[0], ActiveState: lines[1], SubState: lines[2], Description: lines[3],
		Protected: IsProtected(unitName),
	}, nil
}
