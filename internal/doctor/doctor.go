// Package doctor responde "¿qué le pasa a este plugin, y a esta máquina
// como entorno de ejecución?" — una sola llamada en vez de juntar con
// 'plugin status' + 'plugin services' + mirar el .env a mano + acordarse
// si psql estaba instalado.
//
// La regla de todo este paquete, que no se negocia: un check dice SOLO lo
// que de verdad pudo comprobar. "declarado" nunca se confunde con
// "forzado". Asterion no corre los plugins en un sandbox de SO —no hay
// seccomp, no hay namespaces de red— así que "permissions.network" en un
// plugin.yaml es una DECLARACIÓN de intención, no una regla que algo esté
// aplicando; un check de seguridad que lo mostrara como "✓ restringido"
// mentiría sobre una garantía que no existe. Donde Asterion sí fuerza algo
// de verdad (un contenedor que levantó él mismo publica solo en
// 127.0.0.1, nunca --privileged) el check lo dice así, porque ahí sí es
// cierto.
package doctor

import (
	"context"
	"sort"
	"time"

	"asterion-core/internal/plugins"
)

// Severity ordena qué tan grave es un hallazgo. El orden del tipo importa:
// se usa para quedarse con "lo peor" de un reporte.
type Severity int

const (
	// NA: no se pudo comprobar nada (falta una herramienta, no aplica a
	// este plugin) — no es ni bueno ni malo, es la ausencia de dato.
	NA Severity = iota
	OK
	Warn
	Fail
)

func (s Severity) String() string {
	switch s {
	case OK:
		return "ok"
	case Warn:
		return "warn"
	case Fail:
		return "fail"
	default:
		return "na"
	}
}

func (s Severity) Marker() string {
	switch s {
	case OK:
		return "✓"
	case Warn:
		return "⚠"
	case Fail:
		return "✗"
	default:
		return "·"
	}
}

// Check es un único hallazgo verificable. Section agrupa la salida (
// "Health", "Environment", "Security", "Networking", "AI", "Deployment")
// — el mismo agrupamiento que separa qué es sobre ESTE plugin de qué es
// sobre la máquina en general.
type Check struct {
	Section  string   `json:"section"`
	Name     string   `json:"name"`
	Severity Severity `json:"severity"`
	Detail   string   `json:"detail"`
}

// Report es el resultado de correr el doctor sobre un plugin (o, sin
// plugin, sobre la máquina sola — ver Scope).
type Report struct {
	// Scope identifica sobre qué se corrió: el nombre de un plugin, o ""
	// para las secciones que son de la máquina (Environment/AI/Deployment)
	// sin ningún plugin de por medio.
	Scope  string    `json:"scope,omitempty"`
	At     time.Time `json:"at"`
	Checks []Check   `json:"checks"`
}

// Worst es la severidad más alta entre todos los checks — lo que decide si
// el resumen de una línea se imprime en verde, amarillo o rojo.
func (r Report) Worst() Severity {
	worst := NA
	for _, c := range r.Checks {
		if c.Severity > worst {
			worst = c.Severity
		}
	}
	return worst
}

// CountBySeverity, para el resumen final ("3 ok, 1 warn, 0 fail").
func (r Report) CountBySeverity(s Severity) int {
	n := 0
	for _, c := range r.Checks {
		if c.Severity == s {
			n++
		}
	}
	return n
}

// BySection agrupa los checks preservando el primer orden en que aparece
// cada sección — para imprimir siempre en el mismo orden sin tener que
// mantener una lista aparte sincronizada a mano.
func (r Report) BySection() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range r.Checks {
		if !seen[c.Section] {
			seen[c.Section] = true
			out = append(out, c.Section)
		}
	}
	return out
}

// ChecksIn devuelve los checks de una sección, en el orden en que se
// corrieron.
func (r Report) ChecksIn(section string) []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Section == section {
			out = append(out, c)
		}
	}
	return out
}

// Run corre TODAS las secciones específicas de un plugin: su salud (y la
// de los servicios que declara), el entorno de lenguaje que necesita, y
// seguridad/red. No incluye las secciones de máquina (Environment del AI,
// Deployment) — esas las agrega quien arma la salida final, una sola vez,
// con MachineReport.
func Run(ctx context.Context, installed plugins.Installed) Report {
	r := Report{Scope: installed.Name, At: time.Now()}

	config, err := plugins.GetConfig(installed.Name)
	if err != nil {
		// Sin config no se puede hacer mucho más, pero no corta el resto:
		// Health/Environment/Security no dependen de ella.
		config = map[string]string{}
	}

	r.Checks = append(r.Checks, checkHealth(ctx, installed)...)
	r.Checks = append(r.Checks, checkServices(ctx, installed, config)...)
	r.Checks = append(r.Checks, checkEnvironment(ctx, installed)...)
	r.Checks = append(r.Checks, checkSecurity(ctx, installed)...)
	return r
}

// RunAll corre Run sobre todos los plugins instalados y devuelve un
// resumen de una línea por cada uno, ordenado por gravedad — lo que
// imprime 'asterion doctor' sin argumento.
func RunAll(ctx context.Context) ([]summaryLine, error) {
	installed, err := plugins.List()
	if err != nil {
		return nil, err
	}
	lines := make([]summaryLine, 0, len(installed))
	for _, p := range installed {
		lines = append(lines, summarize(p.Name, Run(ctx, p)))
	}
	sortSummaries(lines)
	return lines, nil
}

// MachineReport corre las secciones que no dependen de ningún plugin en
// particular (Environment del lenguaje más común, AI, Deployment) — el
// mismo resultado sirve para 'asterion doctor' sin argumentos y como
// contexto compartido en 'asterion doctor <plugin>'.
func MachineReport(ctx context.Context) Report {
	r := Report{At: time.Now()}
	r.Checks = append(r.Checks, checkAI(ctx)...)
	r.Checks = append(r.Checks, checkDeploymentTargets(ctx)...)
	return r
}

// summaryLine es lo que imprime 'asterion doctor' (sin argumento) por cada
// plugin instalado: una línea, no el reporte completo.
type summaryLine struct {
	Plugin string
	Worst  Severity
	Issue  string // el primer check que no es OK/NA, para dar una pista concreta
}

func summarize(name string, r Report) summaryLine {
	worst := r.Worst()
	s := summaryLine{Plugin: name, Worst: worst}
	if worst < Warn {
		return s
	}
	// El primer check que iguala la severidad MÁS ALTA del reporte — no el
	// primer Warn-o-peor que aparezca. Si hay un Warn de ambiente y un Fail
	// de seguridad, mostrar el de ambiente en la línea de resumen (aunque
	// el marcador ✗ ya diga "grave") escondería justo lo que más importa.
	for _, c := range r.Checks {
		if c.Severity == worst {
			s.Issue = c.Name + ": " + c.Detail
			break
		}
	}
	return s
}

// sortSummaries pone primero lo que necesita atención — el orden en que
// alguien realmente quiere leer una lista de 20 plugins.
func sortSummaries(lines []summaryLine) {
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].Worst != lines[j].Worst {
			return lines[i].Worst > lines[j].Worst
		}
		return lines[i].Plugin < lines[j].Plugin
	})
}
