package plugins

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Build compila el binario de un plugin ya instalado (según lo que declara
// su manifest) y, si tiene un frontend propio (frontend/package.json),
// también lo compila. Es un paso explícito a propósito — nunca se corre
// solo al instalar o arrancar un plugin: Asterion no ejecuta código de un
// repo de terceros sin que el operador lo pida puntualmente, ni siquiera
// para compilarlo (mismo criterio que Install: nunca corre nada del repo
// salvo leer plugin.yaml). `asterion plugin build` es ese pedido explícito.
func Build(name string) (string, error) {
	installed, err := Get(name)
	if err != nil {
		return "", err
	}

	langName := "(no declarado)"
	if installed.Manifest.Language != nil {
		langName = installed.Manifest.Language.Name
	}
	if langName != "go" {
		return "", fmt.Errorf(
			"no sé cómo compilar un plugin de lenguaje %q — hoy 'asterion plugin build' solo soporta "+
				"lenguaje 'go' (declarado en plugin.yaml -> language.name); compilalo a mano según las "+
				"instrucciones del propio plugin", langName,
		)
	}

	outputName := strings.TrimPrefix(installed.Manifest.Start.Command, "./")
	if outputName == "" {
		return "", fmt.Errorf("plugin.yaml no declara start.command, no sé qué binario generar")
	}

	var log strings.Builder

	goBuild := exec.Command("go", "build", "-o", outputName, ".")
	goBuild.Dir = installed.Dir
	out, err := goBuild.CombinedOutput()
	fmt.Fprintf(&log, "$ go build -o %s .   (en %s)\n%s\n", outputName, installed.Dir, out)
	if err != nil {
		return log.String(), fmt.Errorf("go build falló: %w", err)
	}

	frontendDir := filepath.Join(installed.Dir, "frontend")
	if info, statErr := os.Stat(filepath.Join(frontendDir, "package.json")); statErr == nil && !info.IsDir() {
		if _, err := exec.LookPath("pnpm"); err != nil {
			fmt.Fprintf(&log, "\nEste plugin tiene un frontend propio (frontend/package.json) pero no "+
				"encontré 'pnpm' en el PATH — instalalo y corré 'asterion plugin build %s' de nuevo para "+
				"compilarlo también.\n", name)
			return log.String(), nil
		}

		install := exec.Command("pnpm", "install")
		install.Dir = frontendDir
		out, err := install.CombinedOutput()
		fmt.Fprintf(&log, "\n$ pnpm install   (en %s)\n%s\n", frontendDir, out)
		if err != nil {
			return log.String(), fmt.Errorf("pnpm install del frontend falló: %w", err)
		}

		build := exec.Command("pnpm", "build")
		build.Dir = frontendDir
		out, err = build.CombinedOutput()
		fmt.Fprintf(&log, "\n$ pnpm build   (en %s)\n%s\n", frontendDir, out)
		if err != nil {
			return log.String(), fmt.Errorf("pnpm build del frontend falló: %w", err)
		}
	}

	return log.String(), nil
}
