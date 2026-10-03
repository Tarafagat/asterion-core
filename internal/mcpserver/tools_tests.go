package mcpserver

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"asterion-core/internal/plugins"
)

const testTimeout = 2 * time.Minute

// toolRunTests corre el comando de test CONVENCIONAL del lenguaje
// declarado, dentro de la carpeta del plugin ya instalado — nunca un
// comando que el agente pase como texto. Es a propósito: la tool existe
// para que un agente pueda confirmar "¿sigue pasando?" sin que eso sea una
// puerta a ejecutar código arbitrario con el mismo acceso que tiene
// Asterion — la superficie es un comando fijo por lenguaje, igual de
// acotada que 'plugin build' (que tampoco acepta un comando del llamador).
func toolRunTests() Tool {
	return Tool{
		Name:        "run_tests",
		Description: "Corre el test runner convencional del lenguaje del plugin ('go test ./...', 'pytest', o 'npm test' si declara ese script) dentro de su propia carpeta, y devuelve si pasó.",
		InputSchema: map[string]any{
			"type":       "object",
			"required":   []string{"name"},
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
		},
		Handler: func(ctx context.Context, args map[string]any) Result {
			name := argString(args, "name", "")
			if name == "" {
				return fail("falta 'name'")
			}
			installed, err := plugins.Get(name)
			if err != nil {
				return fail(err.Error())
			}

			bin, cmdArgs, err := testCommandFor(installed)
			if err != nil {
				return fail(err.Error())
			}

			runCtx, cancel := context.WithTimeout(ctx, testTimeout)
			defer cancel()
			cmd := exec.CommandContext(runCtx, bin, cmdArgs...)
			cmd.Dir = installed.Dir
			out, runErr := cmd.CombinedOutput()

			text := fmt.Sprintf("$ %s %s  (en %s)\n\n%s", bin, joinArgs(cmdArgs), installed.Dir, truncate(string(out), 8000))
			if runCtx.Err() != nil {
				return fail(text + fmt.Sprintf("\n\n(no terminó en %s)", testTimeout))
			}
			if runErr != nil {
				return fail(text)
			}
			return ok(text)
		},
	}
}

func testCommandFor(installed plugins.Installed) (bin string, args []string, err error) {
	if installed.Manifest.Language == nil {
		return "", nil, fmt.Errorf("%q no declara language.name — no sé qué test runner le corresponde", installed.Name)
	}
	switch installed.Manifest.Language.Name {
	case "go":
		return "go", []string{"test", "./..."}, nil
	case "python":
		if _, lookErr := exec.LookPath("pytest"); lookErr == nil {
			return "pytest", nil, nil
		}
		return "", nil, fmt.Errorf("declara 'python' pero no encontré 'pytest' en el PATH")
	default:
		return "", nil, fmt.Errorf("no sé qué test runner usar para language.name=%q", installed.Manifest.Language.Name)
	}
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n… (cortado, %d bytes más)", len(s)-n)
}
