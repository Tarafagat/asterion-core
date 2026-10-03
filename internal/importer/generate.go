package importer

import (
	"fmt"
	"strconv"
	"strings"
)

// Generate renderiza el app.asterion a partir de lo que Run() encontró.
// Cada llamada Contract.* sale con un comentario arriba que dice de dónde
// salió el dato y con qué confianza — alguien que lea el archivo generado
// tiene que poder distinguir "esto lo dijo tu Dockerfile" de "esto lo
// adiviné por una convención", sin tener que volver a correr 'import' y
// mirar la consola.
func Generate(s *Scan) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("language \"0.1\"\n\n")
	w("# Generado por 'asterion import %s' a partir de lo que ya tenía el proyecto.\n", relOrDot(s.Dir))
	w("# Revisa cada comentario antes de compilarlo: marca qué se detectó con confianza\n")
	w("# y qué es una convención o quedó sin resolver.\n")
	w("#   asterion plugin from-asterion app.asterion --out .\n")
	w("#   asterion plugin validate .\n\n")

	w("Contract.define(\n")
	w("    name=%q,  # %s\n", s.Name.Value, originOf(s.Name))
	w("    version=%q,  # %s\n", s.Version.Value, originOf(s.Version))
	w("    description=\"TODO: describir qué hace este plugin\",\n")
	w(")\n\n")

	if s.Language.Value != "" {
		w("Contract.language(name=%q", s.Language.Value)
		if s.LangVer.Value != "" {
			w(", version=%q", s.LangVer.Value)
		}
		w(")  # %s\n\n", originOf(s.Language))
	} else {
		w("# ⚠ NO SE DETECTÓ el lenguaje del backend — agregar a mano:\n")
		w("# Contract.language(name=\"go\"|\"python\", version=\"...\")\n\n")
	}

	if s.Start.Value != "" {
		cmd, args := splitCommand(s.Start.Value)
		w("Contract.start(command=%q", cmd)
		if len(args) > 0 {
			w(", args=[%s]", quotedList(args))
		}
		w(", port=%s)  # %s\n\n", s.Port.Value, originOf(s.Start))
	} else {
		w("# ⚠ NO PUDE INFERIR el comando de arranque — completar antes de compilar:\n")
		w("Contract.start(command=\"REEMPLAZAR_ESTO\", port=%s)\n\n", s.Port.Value)
	}

	if len(s.Configs) > 0 {
		w("# --- Variables de configuración (de %s) ---\n", configSources(s))
		for _, c := range s.Configs {
			w("Contract.config(key=%q, type=%q, required=%s", c.Key, c.Type, strconv.FormatBool(c.Required))
			if c.Default != "" && !c.Required {
				w(", default=%q", c.Default)
			}
			w(")\n")
		}
		w("\n")
	}

	for _, svc := range s.Services {
		w("# Servicio detectado: %s (%s) — %s\n", svc.Name, svc.Kind, svc.Source)
		w("Contract.service(\n")
		w("    name=%q,\n", svc.Name)
		w("    kind=%q,\n", svc.Kind)
		if svc.Version != "" {
			w("    version=%q,\n", svc.Version)
		}
		if svc.Database != "" {
			w("    database=%q,\n", svc.Database)
		}
		if svc.User != "" {
			w("    user=%q,\n", svc.User)
		}
		// Orden fijo, no un map: dos corridas sobre el mismo proyecto
		// tienen que generar el mismo texto, para que un diff entre ellas
		// muestre solo lo que de verdad cambió.
		for _, m := range []struct{ key, val string }{
			{"maps_host", svc.MapsHost}, {"maps_port", svc.MapsPort}, {"maps_user", svc.MapsUser},
			{"maps_password", svc.MapsPassword}, {"maps_database", svc.MapsDatabase}, {"maps_url", svc.MapsURL},
		} {
			if m.val != "" {
				w("    %s=%q,\n", m.key, m.val)
			}
		}
		if svc.MapsHost == "" && svc.MapsURL == "" {
			w("    # ⚠ no encontré a qué variable volcar la conexión — completar maps_host/maps_port o maps_url,\n")
			w("    # y declarar esa clave arriba con Contract.config(key=..., type=\"string\"|\"secret\")\n")
		}
		w(")\n\n")
	}

	return b.String()
}

func originOf(f Finding) string {
	if f.Confidence == Declared {
		return "detectado en " + f.Source
	}
	return "adivinado (" + f.Source + ") — revisar"
}

func configSources(s *Scan) string {
	seen := map[string]bool{}
	var out []string
	for _, c := range s.Configs {
		if !seen[c.Source] {
			seen[c.Source] = true
			out = append(out, c.Source)
		}
	}
	return strings.Join(out, ", ")
}

func relOrDot(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

// splitCommand separa "npm start" en comando="npm" args=["start"] — lo
// que Contract.start(command=, args=) espera, en vez de un solo string con
// espacios que un shell tendría que volver a partir.
func splitCommand(full string) (cmd string, args []string) {
	fields := strings.Fields(full)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}

func quotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = strconv.Quote(it)
	}
	return strings.Join(quoted, ", ")
}
