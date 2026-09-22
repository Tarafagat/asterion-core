package sysservices

import (
	"fmt"
	"os/exec"
)

// List corre `systemctl list-units --all --type=service`, prefiriendo
// --output=json (systemd moderno, ~v247+) y cayendo a texto plano si el
// flag no existe (systemd viejo — todavía común en producción: Ubuntu
// 18.04/20.04, Debian 10/11). Nunca requiere sudo: listar/inspeccionar
// unidades es una operación sin privilegios.
func List() ([]Unit, error) {
	if out, err := exec.Command("systemctl", "list-units", "--all", "--type=service", "--output=json", "--no-pager").Output(); err == nil {
		if units, parseErr := parseJSON(out); parseErr == nil {
			return annotate(units), nil
		}
		// --output=json existe pero devolvió algo inesperado: cae al
		// parser de texto en vez de fallar de punta a punta.
	}
	out, err := exec.Command("systemctl", "list-units", "--all", "--type=service", "--no-legend", "--no-pager", "--plain", "--full").Output()
	if err != nil {
		return nil, fmt.Errorf("no se pudo listar unidades systemd: %w", err)
	}
	return annotate(parsePlain(out)), nil
}

func annotate(units []Unit) []Unit {
	for i := range units {
		units[i].Protected = IsProtected(units[i].Name)
		units[i].Category = Classify(units[i].Name)
	}
	return units
}

// Get consulta UNA unidad puntual por nombre — sin sudo (leer estado nunca
// lo necesita), mismo camino que usa Control() para "verificar" después de
// actuar (ver status() en control.go). Sirve para nombrar una unidad
// directo (`asterion services list <nombre>`) sin tener que listar
// todas para filtrar una sola. Un nombre bien formado pero inexistente no
// es un error: systemctl devuelve LoadState "not-found" igual que
// cualquier otra consulta (mismo criterio que parsePlain ya contempla).
func Get(unitName string) (Unit, error) {
	if !ValidUnitName(unitName) {
		return Unit{}, fmt.Errorf("nombre de unidad inválido: %q", unitName)
	}
	return status(unitName)
}
