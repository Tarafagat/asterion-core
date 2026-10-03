package importer

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// detectName busca el nombre declarado del proyecto antes de caer al
// nombre de la carpeta — package.json/pyproject.toml casi siempre lo
// tienen, y es mejor nombre que "proyecto-final-v2".
func detectName(s *Scan) Finding {
	if text, ok := readFile(s.Dir, "package.json"); ok {
		var doc struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(text), &doc) == nil && doc.Name != "" {
			return Finding{Value: sanitizeName(doc.Name), Confidence: Declared, Source: "package.json"}
		}
	}
	if text, ok := readFile(s.Dir, "pyproject.toml"); ok {
		if name := tomlString(text, "project", "name"); name != "" {
			return Finding{Value: sanitizeName(name), Confidence: Declared, Source: "pyproject.toml"}
		}
		if name := tomlString(text, "tool.poetry", "name"); name != "" {
			return Finding{Value: sanitizeName(name), Confidence: Declared, Source: "pyproject.toml"}
		}
	}
	if text, ok := readFile(s.Dir, "go.mod"); ok {
		if m := goModuleRE.FindStringSubmatch(text); m != nil {
			last := m[1]
			if i := strings.LastIndexByte(last, '/'); i >= 0 {
				last = last[i+1:]
			}
			return Finding{Value: sanitizeName(last), Confidence: Declared, Source: "go.mod"}
		}
	}
	if text, ok := readFile(s.Dir, "Cargo.toml"); ok {
		if name := tomlString(text, "package", "name"); name != "" {
			return Finding{Value: sanitizeName(name), Confidence: Declared, Source: "Cargo.toml"}
		}
	}
	base := filepath.Base(s.Dir)
	return Finding{Value: sanitizeName(base), Confidence: Guessed, Source: "nombre de la carpeta"}
}

var goModuleRE = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// sanitizeName deja el nombre apto para ser el "name" de un plugin
// (minúsculas, guiones) — un "Mi App!" de un package.json termina siendo
// "mi-app", no un literal que después rompa en algún lado por un espacio.
func sanitizeName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "mi-plugin"
	}
	return out
}

// tomlString lee key = "value" (o key = valor sin comillas) dentro de
// [section] — el subconjunto mínimo de TOML que hace falta acá. No es un
// parser de TOML completo: no hace falta uno para leer cuatro campos
// conocidos, y sumar una dependencia nueva por esto no vale la pena (mismo
// criterio que el resto del proyecto con `stty` en vez de un paquete de
// terminal).
func tomlString(text, section, key string) string {
	lines := strings.Split(text, "\n")
	inSection := section == "" // "" = buscar en cualquier lado, antes de la primera sección
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			name := strings.Trim(trimmed, "[]")
			inSection = name == section
			continue
		}
		if !inSection {
			continue
		}
		k, v, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return ""
}
