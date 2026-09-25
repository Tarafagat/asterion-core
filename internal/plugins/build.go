package plugins

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Build prepara el backend de un plugin ya instalado (según lo que declara
// su manifest — compilar si es Go, sincronizar el venv si es Python) y, si
// tiene un frontend propio (frontend/package.json), también lo compila. Es
// un paso explícito a propósito — nunca se corre solo al instalar o
// arrancar un plugin: Asterion no ejecuta código de un repo de terceros
// sin que el operador lo pida puntualmente, ni siquiera para prepararlo
// (mismo criterio que Install: nunca corre nada del repo salvo leer
// plugin.yaml). `asterion plugin build`/`plugin start --build`/`plugin
// restart --build` son ese pedido explícito.
func Build(name string) (string, error) {
	installed, err := Get(name)
	if err != nil {
		return "", err
	}

	langName := "(no declarado)"
	if installed.Manifest.Language != nil {
		langName = installed.Manifest.Language.Name
	}

	var log strings.Builder

	switch langName {
	case "go":
		if err := buildGo(installed, &log); err != nil {
			return log.String(), err
		}
	case "python":
		if err := buildPython(installed, &log); err != nil {
			return log.String(), err
		}
	default:
		return "", fmt.Errorf(
			"no sé cómo preparar un plugin de lenguaje %q — hoy 'asterion plugin build' solo soporta "+
				"'go' y 'python' (declarado en plugin.yaml -> language.name); compilalo a mano según las "+
				"instrucciones del propio plugin", langName,
		)
	}

	if err := buildFrontend(installed, &log); err != nil {
		return log.String(), err
	}
	return log.String(), nil
}

// buildGo compila el binario Go de start.command con `go build`.
func buildGo(installed Installed, log *strings.Builder) error {
	outputName := strings.TrimPrefix(installed.Manifest.Start.Command, "./")
	if outputName == "" {
		return fmt.Errorf("plugin.yaml no declara start.command, no sé qué binario generar")
	}

	// Todo plugin Go real referencia asterion-plugin-contract con
	// 'replace ../asterion-plugin-contract' en su propio go.mod (por ser
	// justamente un plugin que implementa ese contrato) — sin la copia
	// compartida al lado (ver EnsureContractRepo), 'go build' falla acá
	// mismo con "replacement directory ../asterion-plugin-contract does
	// not exist", visto en vivo en una instancia recién clonada.
	if err := EnsureContractRepo(); err != nil {
		return fmt.Errorf("no pude preparar asterion-plugin-contract (lo necesita este plugin para compilar): %w", err)
	}

	goBuild := exec.Command("go", "build", "-o", outputName, ".")
	goBuild.Dir = installed.Dir
	out, err := goBuild.CombinedOutput()
	fmt.Fprintf(log, "$ go build -o %s .   (en %s)\n%s\n", outputName, installed.Dir, out)
	if err != nil {
		return fmt.Errorf("go build falló: %w", err)
	}
	return nil
}

// buildPython sincroniza el venv de un plugin Python. Dos formas de
// ubicar el venv y requirements.txt, en este orden:
//
//  1. Explícita: si plugin.yaml declara `language.venv`/`language.requirements`
//     (ver Contract.language(..., venv=..., requirements=...) en
//     asterion-language), se usan esas rutas tal cual, relativas a la raíz
//     del plugin.
//  2. Por convención (sin cambios respecto de como funcionaba antes de que
//     existiera la forma explícita, y sigue siendo válida sin declarar
//     nada): start.command apunta directo al intérprete DENTRO del venv
//     (ej. "./backend/venv/bin/python"), y requirements.txt vive como
//     hermano del propio venv (ej. "backend/requirements.txt", junto a
//     "backend/venv/") — mismo convenio que ya usa asterion-sii.
//
// Si el venv todavía no existe (clone recién hecho), lo crea con
// `python3 -m venv` antes de instalar — mismo espíritu que buildGo, que
// tampoco asume que el binario ya esté compilado.
func buildPython(installed Installed, log *strings.Builder) error {
	venvDir, requirementsPath, explicit, err := pythonVenvPaths(installed)
	if err != nil {
		return err
	}
	venvBinDir := filepath.Join(venvDir, venvBinSubdir()) // ej. "backend/venv/bin" ("Scripts" en Windows)
	if !explicit {
		// Camino por convención: preserva el subdirectorio real que ya
		// declaró start.command (siempre "bin"/"Scripts" en la práctica,
		// pero deriva de ahí en vez de asumirlo — sin cambios de
		// comportamiento respecto de antes de que existiera la forma
		// explícita).
		venvBinDir = filepath.Dir(installed.Manifest.Start.Command)
	}

	pipAbsPath := filepath.Join(installed.Dir, venvBinDir, "pip")
	if _, err := os.Stat(pipAbsPath); err != nil {
		python, err := pythonInterpreter()
		if err != nil {
			return err
		}
		createVenv := exec.Command(python, "-m", "venv", venvDir)
		createVenv.Dir = installed.Dir
		out, err := createVenv.CombinedOutput()
		fmt.Fprintf(log, "$ %s -m venv %s   (en %s)\n%s\n", python, venvDir, installed.Dir, out)
		if err != nil {
			return fmt.Errorf("no pude crear el venv: %w", err)
		}
	}

	if _, err := os.Stat(filepath.Join(installed.Dir, requirementsPath)); err != nil {
		fmt.Fprintf(log, "\n(no encontré %s — nada que instalar, sigo con el venv tal como está)\n", requirementsPath)
		return nil
	}

	pipInstall := exec.Command(pipAbsPath, "install", "-r", requirementsPath)
	pipInstall.Dir = installed.Dir
	out, err := pipInstall.CombinedOutput()
	fmt.Fprintf(log, "\n$ %s/pip install -r %s   (en %s)\n%s\n", venvBinDir, requirementsPath, installed.Dir, out)
	if err != nil {
		return fmt.Errorf("pip install falló: %w", err)
	}
	return nil
}

// pythonVenvPaths decide dónde está (o va a crearse) el venv y
// requirements.txt de un plugin Python — ver el doc comment de
// buildPython para las dos formas (explícita vs. por convención).
// explicit=true cuando vino de plugin.yaml -> language.venv, para que
// buildPython sepa qué subdirectorio de binarios asumir (ver
// venvBinSubdir) en vez de derivarlo de start.command.
func pythonVenvPaths(installed Installed) (venvDir, requirementsPath string, explicit bool, err error) {
	if lang := installed.Manifest.Language; lang != nil && lang.Venv != "" {
		venvDir = lang.Venv
		requirementsPath = lang.Requirements
		if requirementsPath == "" {
			requirementsPath = filepath.Join(filepath.Dir(venvDir), "requirements.txt")
		}
		return venvDir, requirementsPath, true, nil
	}

	venvBinDir := filepath.Dir(installed.Manifest.Start.Command) // ej. "backend/venv/bin"
	venvDir = filepath.Dir(venvBinDir)                           // ej. "backend/venv"
	if venvDir == "." || venvDir == "/" {
		return "", "", false, fmt.Errorf(
			"start.command (%q) no tiene la forma esperada '<algo>/venv/bin/python', y plugin.yaml tampoco "+
				"declaró Contract.language(..., venv=..., requirements=...) explícito — no puedo ubicar el "+
				"venv ni requirements.txt de ninguna de las dos formas", installed.Manifest.Start.Command,
		)
	}
	requirementsPath = filepath.Join(filepath.Dir(venvDir), "requirements.txt") // ej. "backend/requirements.txt"
	return venvDir, requirementsPath, false, nil
}

// venvBinSubdir es "Scripts" en Windows, "bin" en todo lo demás — mismo
// layout que `python3 -m venv` crea solo, según el SO donde corre. Solo
// hace falta adivinarlo para el camino EXPLÍCITO (language.venv): el
// camino por convención ya lo deriva directo de start.command, sin
// adivinar nada.
func venvBinSubdir() string {
	if runtime.GOOS == "windows" {
		return "Scripts"
	}
	return "bin"
}

// PythonInterpreter es pythonInterpreter expuesto — lo usa 'asterion
// plugin system apply/export' (ver requires=["python"] en System.plugin)
// para VERIFICAR que Python está disponible antes de construir, sin
// descargar nada (a diferencia de EnsureNode/EnsurePnpm): Python no
// publica un tarball portable oficial por versión exacta como sí hace
// Node — "instalarlo sandboxed" no es un problema resuelto acá, solo
// confirmar que ya está y fallar claro si no.
func PythonInterpreter() (string, error) { return pythonInterpreter() }

// pythonInterpreter busca 'python3' y cae a 'python' — no asume una
// versión puntual (a diferencia de Go, que sí fija su toolchain en
// go.mod, Python típicamente resuelve la versión vía el propio venv/
// pyenv del sistema, fuera del alcance de este comando).
func pythonInterpreter() (string, error) {
	if p, err := exec.LookPath("python3"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("python"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("no encontré 'python3' ni 'python' en el PATH — hace falta para crear el venv del plugin")
}

// buildFrontend compila el frontend propio del plugin (si tiene) — mismo
// paso sea cual sea el lenguaje del backend, así que vive separado de
// buildGo/buildPython en vez de duplicado en cada uno.
func buildFrontend(installed Installed, log *strings.Builder) error {
	frontendDir := filepath.Join(installed.Dir, "frontend")
	if info, statErr := os.Stat(filepath.Join(frontendDir, "package.json")); statErr == nil && !info.IsDir() {
		if _, err := exec.LookPath("pnpm"); err != nil {
			fmt.Fprintf(log, "\nEste plugin tiene un frontend propio (frontend/package.json) pero no "+
				"encontré 'pnpm' en el PATH — instalalo y corré 'asterion plugin build %s' de nuevo para "+
				"compilarlo también.\n", installed.Name)
			return nil
		}

		install := exec.Command("pnpm", "install")
		install.Dir = frontendDir
		out, err := install.CombinedOutput()
		fmt.Fprintf(log, "\n$ pnpm install   (en %s)\n%s\n", frontendDir, out)
		if err != nil {
			return fmt.Errorf("pnpm install del frontend falló: %w", err)
		}

		build := exec.Command("pnpm", "build")
		build.Dir = frontendDir
		out, err = build.CombinedOutput()
		fmt.Fprintf(log, "\n$ pnpm build   (en %s)\n%s\n", frontendDir, out)
		if err != nil {
			return fmt.Errorf("pnpm build del frontend falló: %w", err)
		}
	}

	return nil
}
