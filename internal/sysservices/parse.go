package sysservices

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
)

// parseJSON interpreta la salida de `systemctl list-units --output=json`
// (systemd ~v247+): un array de objetos con las mismas 5 columnas que la
// salida de texto plano, ya separadas.
func parseJSON(out []byte) ([]Unit, error) {
	var raw []struct {
		Unit        string `json:"unit"`
		Load        string `json:"load"`
		Active      string `json:"active"`
		Sub         string `json:"sub"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	units := make([]Unit, 0, len(raw))
	for _, r := range raw {
		units = append(units, Unit{Name: r.Unit, LoadState: r.Load, ActiveState: r.Active, SubState: r.Sub, Description: r.Description})
	}
	return units, nil
}

// parsePlain interpreta `systemctl list-units --no-legend --plain --full`:
// columnas UNIT LOAD ACTIVE SUB DESCRIPTION separadas por espacios de ancho
// variable (alineación, no un separador fijo) — por eso se usa
// strings.Fields (colapsa cualquier corrida de espacios) para las primeras
// 4 columnas, y se reúne todo lo que sobra para DESCRIPTION en vez de un
// SplitN ingenuo, que rompería con espacios múltiples de alineación.
// --no-legend ya saca el header y el footer ("N loaded units listed.'); el
// chequeo de len(fields) < 4 es una red extra por si igual se cuela una
// línea en blanco.
func parsePlain(out []byte) []Unit {
	var units []Unit
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		u := Unit{Name: fields[0], LoadState: fields[1], ActiveState: fields[2], SubState: fields[3]}
		if len(fields) > 4 {
			u.Description = strings.Join(fields[4:], " ")
		}
		units = append(units, u)
	}
	return units
}
