package importer

import (
	"encoding/json"
	"regexp"
	"strings"
)

// detectLanguageAndStart es la parte que más señales cruza: el lenguaje
// decide si 'asterion plugin build' va a poder preparar el plugin (hoy
// solo sabe go/python — node y rust se detectan igual, para que el
// app.asterion quede completo, pero con el aviso explícito de que el
// build hay que hacerlo a mano), y el comando de arranque sale de
// cualquiera de varias fuentes, en el orden de confianza real:
//
//  1. Dockerfile (CMD/ENTRYPOINT) — ya es LA forma en que ese proyecto se
//     arranca en producción, nadie lo adivinó.
//  2. Procfile ("web: ...") — convención de Heroku/Railway, tan directa
//     como el Dockerfile.
//  3. package.json "scripts.start" — declarado, aunque indirecto (corre
//     "npm start", no el proceso final).
//  4. Convención de Asterion para Go: un go.mod sin ninguna de las
//     anteriores asume que el binario compilado se llama como el módulo.
//  5. Un entrypoint de Python reconocible por nombre de archivo — la más
//     débil: puede estar equivocada, se marca Guessed.
func detectLanguageAndStart(s *Scan) {
	detectLanguage(s)
	if f, ok := startFromDockerfile(s); ok {
		s.Start = f
		return
	}
	if f, ok := startFromProcfile(s); ok {
		s.Start = f
		return
	}
	if f, ok := startFromPackageJSON(s); ok {
		s.Start = f
		return
	}
	if s.Language.Value == "go" {
		if f, ok := startFromGoConvention(s); ok {
			s.Start = f
			return
		}
	}
	if s.Language.Value == "python" {
		if f, ok := startFromPythonEntrypoint(s); ok {
			s.Start = f
			return
		}
	}
	s.Start = Finding{} // sin nada — Run() lo convierte en warning
}

func detectLanguage(s *Scan) {
	if text, ok := readFile(s.Dir, "go.mod"); ok {
		s.sawFile("go.mod")
		s.Language = Finding{Value: "go", Confidence: Declared, Source: "go.mod"}
		if m := goVersionRE.FindStringSubmatch(text); m != nil {
			s.LangVer = Finding{Value: m[1], Confidence: Declared, Source: "go.mod"}
		}
		return
	}
	if s.has("requirements.txt") || s.has("pyproject.toml") || s.has("setup.py") || s.has("Pipfile") {
		s.Language = Finding{Value: "python", Confidence: Declared, Source: pythonSource(s)}
		if text, ok := readFile(s.Dir, "pyproject.toml"); ok {
			if v := tomlString(text, "project", "requires-python"); v != "" {
				s.LangVer = Finding{Value: strings.TrimLeft(v, "<>=^~ "), Confidence: Declared, Source: "pyproject.toml"}
			}
		}
		return
	}
	if s.has("Cargo.toml") {
		s.sawFile("Cargo.toml")
		s.Language = Finding{Value: "rust", Confidence: Declared, Source: "Cargo.toml"}
		s.warn("detecté Rust (Cargo.toml) — Asterion hoy compila 'go' y 'python' con 'plugin build'; para Rust vas a compilar vos y declarar start.command apuntando al binario ya armado")
		return
	}
	if text, ok := readFile(s.Dir, "package.json"); ok {
		s.sawFile("package.json")
		var doc struct {
			Engines struct{ Node string `json:"node"` } `json:"engines"`
		}
		json.Unmarshal([]byte(text), &doc)
		s.Language = Finding{Value: "node", Confidence: Declared, Source: "package.json"}
		if doc.Engines.Node != "" {
			s.LangVer = Finding{Value: doc.Engines.Node, Confidence: Declared, Source: "package.json (engines.node)"}
		}
		s.warn("detecté Node como backend (package.json, sin go.mod/requirements.txt) — Asterion hoy compila 'go' y 'python' con 'plugin build'; para Node instalá las dependencias vos mismo (npm ci) antes de 'plugin start', o declarálo en un Dockerfile propio")
		return
	}
	// Language queda vacío — Run() ya lo marca como warning.
}

func pythonSource(s *Scan) string {
	for _, f := range []string{"pyproject.toml", "requirements.txt", "Pipfile", "setup.py"} {
		if s.has(f) {
			s.sawFile(f)
			return f
		}
	}
	return ""
}

func (s *Scan) has(rel string) bool {
	_, ok := readFile(s.Dir, rel)
	return ok
}

var goVersionRE = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+)`)

// dockerfileCmdRE agarra tanto la forma JSON ("exec form", CMD
// ["python3","app.py"]) como la forma shell (CMD python3 app.py). Toma la
// ÚLTIMA ocurrencia del archivo: en un Dockerfile multi-stage, la del
// último stage es la que de verdad corre.
var dockerfileCmdRE = regexp.MustCompile(`(?mi)^\s*(CMD|ENTRYPOINT)\s+(.+?)\s*$`)

func startFromDockerfile(s *Scan) (Finding, bool) {
	text, ok := readFile(s.Dir, "Dockerfile")
	if !ok {
		return Finding{}, false
	}
	s.sawFile("Dockerfile")

	matches := dockerfileCmdRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return Finding{}, false
	}
	last := matches[len(matches)-1][2]
	cmd := parseDockerInstructionArgs(last)
	if cmd == "" {
		return Finding{}, false
	}

	if m := exposeRE.FindStringSubmatch(text); m != nil && s.Port.Value == "" {
		s.Port = Finding{Value: m[1], Confidence: Declared, Source: "Dockerfile (EXPOSE)"}
	}
	return Finding{Value: cmd, Confidence: Declared, Source: "Dockerfile"}, true
}

var exposeRE = regexp.MustCompile(`(?mi)^\s*EXPOSE\s+(\d+)`)

// parseDockerInstructionArgs interpreta tanto ["a","b"] (exec form) como
// 'a b' (shell form) y devuelve "a b" listo para partir en command+args.
func parseDockerInstructionArgs(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") {
		var parts []string
		if json.Unmarshal([]byte(raw), &parts) == nil {
			return strings.Join(parts, " ")
		}
		return ""
	}
	return raw
}

var procfileWebRE = regexp.MustCompile(`(?mi)^\s*web:\s*(.+?)\s*$`)

func startFromProcfile(s *Scan) (Finding, bool) {
	text, ok := readFile(s.Dir, "Procfile")
	if !ok {
		return Finding{}, false
	}
	s.sawFile("Procfile")
	m := procfileWebRE.FindStringSubmatch(text)
	if m == nil {
		return Finding{}, false
	}
	return Finding{Value: m[1], Confidence: Declared, Source: "Procfile"}, true
}

func startFromPackageJSON(s *Scan) (Finding, bool) {
	text, ok := readFile(s.Dir, "package.json")
	if !ok {
		return Finding{}, false
	}
	var doc struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal([]byte(text), &doc) != nil || doc.Scripts["start"] == "" {
		return Finding{}, false
	}
	return Finding{Value: "npm start", Confidence: Declared, Source: "package.json (scripts.start)"}, true
}

// startFromGoConvention asume el mismo patrón que ya usan los plugins de
// este propio ecosistema (ver asterion-mail-plugin-basic):
// start.command="./<nombre>", el binario que 'plugin build' deja
// compilado ahí mismo con `go build -o <nombre>`.
func startFromGoConvention(s *Scan) (Finding, bool) {
	if s.Name.Value == "" {
		return Finding{}, false
	}
	return Finding{Value: "./" + s.Name.Value, Confidence: Guessed,
		Source: "convención: 'go build' deja el binario con el nombre del módulo"}, true
}

var pythonEntrypoints = []string{"main.py", "app.py", "wsgi.py", "asgi.py", "manage.py", "run.py"}

func startFromPythonEntrypoint(s *Scan) (Finding, bool) {
	for _, f := range pythonEntrypoints {
		if s.has(f) {
			s.sawFile(f)
			return Finding{Value: "python3 " + f, Confidence: Guessed, Source: "se encontró " + f}, true
		}
	}
	return Finding{}, false
}
