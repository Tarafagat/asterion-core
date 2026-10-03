package importer

import (
	"strconv"
	"strings"
)

// envPair es una línea KEY=VALUE ya parseada de un .env.example.
type envPair struct {
	Key   string
	Value string
}

// secretNameHints son fragmentos del NOMBRE de una clave que, si
// aparecen, hacen que se declare type="secret" — es una señal sobre el
// nombre, no sobre el valor (un .env.example nunca debería tener el
// secreto real adentro, así que el valor no sirve para esto).
var secretNameHints = []string{"SECRET", "PASSWORD", "PASSWD", "TOKEN", "PRIVATE_KEY", "API_KEY", "APIKEY", "CREDENTIAL"}

// envFiles son los nombres de archivo que proyectos reales usan para
// documentar sus variables sin comprometer ninguna — en ese orden de
// preferencia (si hay varios, se usa el primero que exista).
var envFiles = []string{".env.example", ".env.sample", ".env.template", ".env.dist"}

func parseEnvFile(text string) []envPair {
	var out []envPair
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" || !isEnvKey(k) {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		out = append(out, envPair{Key: k, Value: v})
	}
	return out
}

func isEnvKey(k string) bool {
	for _, r := range k {
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return k != ""
}

func envValue(text, key string) string {
	for _, p := range parseEnvFile(text) {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// findEnvFile busca el primer archivo de ejemplo de entorno que exista,
// en envFiles, y devuelve su contenido ya parseado más la ruta que se usó
// — las dos cosas hacen falta en más de un sitio (acá y en
// detect_services.go para DATABASE_URL/REDIS_URL).
func findEnvFile(s *Scan) (string, []envPair) {
	for _, name := range envFiles {
		if text, ok := readFile(s.Dir, name); ok {
			s.sawFile(name)
			return name, parseEnvFile(text)
		}
	}
	return "", nil
}

// detectConfig arma un Contract.config(...) por cada variable del .env de
// ejemplo que NO va a quedar completamente resuelta por un servicio
// detectado (ver detect_services.go, que marca sus propias maps_* y las
// excluye de acá — una clave no puede aparecer dos veces, una vez como
// config suelto y otra como destino de un maps_*, sería confuso y
// redundante).
func detectConfig(s *Scan) {
	source, pairs := findEnvFile(s)
	if source == "" {
		return
	}
	for _, p := range pairs {
		// PORT (o HOST=0.0.0.0, de un servidor que escucha en cualquier
		// interfaz) no es una config del plugin: Asterion ya le inyecta el
		// puerto por ASTERION_PLUGIN_PORT (ver Contract.start(..., port=)
		// más arriba en el archivo generado) — declararla además como
		// Contract.config crearía una clave sin ningún maps_* que la
		// conecte a nada, y alguien podría llenarla pensando que hace
		// algo. Queda como aviso, no como campo.
		if strings.EqualFold(p.Key, "PORT") {
			s.warn("el proyecto declara PORT en %s, pero Asterion ya inyecta el puerto como ASTERION_PLUGIN_PORT — si el código lee process.env.PORT (u os.environ[\"PORT\"]) directamente, hay que ajustarlo para leer ASTERION_PLUGIN_PORT, o remapearla vos mismo antes de arrancar", source)
			continue
		}
		s.Configs = append(s.Configs, ConfigKey{
			Key:      p.Key,
			Type:     inferType(p.Key, p.Value),
			Required: p.Value == "",
			Default:  p.Value,
			Source:   source,
		})
	}
}

func inferType(key, value string) string {
	upper := strings.ToUpper(key)
	for _, hint := range secretNameHints {
		if strings.Contains(upper, hint) {
			return "secret"
		}
	}
	if value == "" {
		return "string"
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return "number"
	}
	if value == "true" || value == "false" {
		return "bool"
	}
	return "string"
}
