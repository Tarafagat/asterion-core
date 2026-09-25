package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"asterion-core/internal/plugins"
)

// pluginExportCmd empaqueta un plugin ya instalado en una carpeta
// portable, autocontenida — "tipo contenedor de Docker" en el sentido de
// "corrible en cualquier lado sin el CLI de Asterion", no un pedido de
// que Asterion corra Docker por su cuenta (se genera un Dockerfile de
// referencia, nunca se ejecuta 'docker build' acá). Reusa Build() para
// dejar todo actualizado antes de empaquetar — no reimplementa nada de
// eso.
func pluginExportCmd() *cobra.Command {
	var out, include string
	var port int
	cmd := &cobra.Command{
		Use:   "export <name>",
		Short: "Empaqueta un plugin ya instalado en una carpeta portable, lista para correr fuera de Asterion",
		Long: "Compila el plugin (ver 'plugin build') y arma una carpeta autocontenida con todo lo\n" +
			"necesario para correrlo SIN el CLI de asterion ni su máquina de estado:\n\n" +
			"  .env_asterion_produced   — las mismas env vars que ya arma 'asterion plugin start'\n" +
			"                             (ASTERION_PLUGIN_NAME/PORT/DIR/CONFIG_*) con los valores\n" +
			"                             REALES ya configurados (asterion plugin config set) —\n" +
			"                             contiene secretos de verdad, permisos 0600, nunca se\n" +
			"                             commitea (el .gitignore generado ya lo excluye).\n" +
			"  frontend/.env.production — SOLO si el plugin tiene frontend propio: SOLO los campos\n" +
			"                             de config_schema marcados explícitamente no-secretos —\n" +
			"                             el build del frontend nunca ve .env_asterion_produced ni\n" +
			"                             ningún secreto real, a propósito (cualquier variable que\n" +
			"                             un bundler exponga al navegador termina siendo pública).\n" +
			"  Dockerfile               — punto de partida para 'docker build' (recompila desde el\n" +
			"                             código fuente copiado, nunca copia el binario ya compilado\n" +
			"                             del host — podría no matchear la arquitectura del\n" +
			"                             contenedor). Asterion nunca corre Docker por su cuenta.\n\n" +
			"Un plugin Python no lleva su venv copiado (los venv no son portables entre máquinas —\n" +
			"referencian rutas absolutas del intérprete original): el README generado explica cómo\n" +
			"recrearlo, o usar el Dockerfile, que sí instala las dependencias desde cero.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginExport(args[0], out, include, port)
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "Directorio de salida (default: ./<name>-export)")
	cmd.Flags().StringVar(&include, "include", "auto", "Qué empaquetar: \"auto\" (both si el plugin tiene frontend propio, si no backend), \"backend\", o \"both\"")
	cmd.Flags().IntVar(&port, "port", 8080, "ASTERION_PLUGIN_PORT a fijar en el .env exportado — el puerto de desarrollo actual no existe fuera de esta máquina")
	return cmd
}

func runPluginExport(name, out, include string, port int) error {
	if out == "" {
		out = "./" + name + "-export"
	}
	outAbs, err := filepath.Abs(out)
	if err != nil {
		return err
	}

	_, _, err = exportPluginTo(name, outAbs, include, port)
	if err != nil {
		return err
	}
	fmt.Printf("\n✓ export de %q listo en %s\n", name, outAbs)
	return nil
}

// exportPluginTo es el export de UN plugin, reusado tanto por
// pluginExportCmd (un plugin puntual) como por pluginSystemExportCmd
// (cada plugin de un sistema, en su propia subcarpeta) — devuelve si
// terminó incluyendo frontend y la config resuelta, que el caller de
// sistema necesita para armar el .env_asterion_produced combinado de
// nivel sistema.
func exportPluginTo(name, outAbs, include string, port int) (includedFrontend bool, config map[string]string, err error) {
	if _, err := plugins.Get(name); err != nil {
		return false, nil, err
	}

	fmt.Printf("Compilando %q...\n", name)
	log, err := plugins.Build(name)
	if log != "" {
		fmt.Println(log)
	}
	if err != nil {
		return false, nil, err
	}

	installed, err := plugins.Get(name)
	if err != nil {
		return false, nil, err
	}

	hasFrontend := hasFrontendDir(installed.Dir)
	includeFrontend, err := resolveInclude(include, hasFrontend)
	if err != nil {
		return false, nil, err
	}

	if err := os.MkdirAll(outAbs, 0o755); err != nil {
		return false, nil, err
	}

	isPython := installed.Manifest.Language != nil && installed.Manifest.Language.Name == "python"
	if err := copyPluginTree(installed.Dir, outAbs, includeFrontend, isPython); err != nil {
		return false, nil, fmt.Errorf("no pude copiar los archivos del plugin: %w", err)
	}

	config, err = plugins.GetConfig(name)
	if err != nil {
		return false, nil, err
	}

	envPath := filepath.Join(outAbs, ".env_asterion_produced")
	if err := writeBackendEnv(envPath, installed, config, port); err != nil {
		return false, nil, err
	}
	fmt.Printf("✓ %s — CONTIENE SECRETOS REALES (permisos 0600, nunca lo commitees)\n", envPath)

	if includeFrontend {
		frontendEnvPath := filepath.Join(outAbs, "frontend", ".env.production")
		if err := writeFrontendSafeEnv(frontendEnvPath, installed, config); err != nil {
			return false, nil, err
		}
		fmt.Printf("✓ %s — solo valores no-secretos, nunca ve .env_asterion_produced\n", frontendEnvPath)
	}

	if err := writeDockerfile(filepath.Join(outAbs, "Dockerfile"), installed, port, includeFrontend, isPython); err != nil {
		return false, nil, err
	}
	if err := writeExportReadme(filepath.Join(outAbs, "README_ASTERION_EXPORT.md"), installed, includeFrontend, isPython); err != nil {
		return false, nil, err
	}
	if err := os.WriteFile(filepath.Join(outAbs, ".gitignore"), []byte(".env_asterion_produced\n"), 0o644); err != nil {
		return false, nil, err
	}

	return includeFrontend, config, nil
}

func hasFrontendDir(pluginDir string) bool {
	info, err := os.Stat(filepath.Join(pluginDir, "frontend", "package.json"))
	return err == nil && !info.IsDir()
}

func resolveInclude(include string, hasFrontend bool) (bool, error) {
	switch include {
	case "auto":
		return hasFrontend, nil
	case "backend":
		return false, nil
	case "both":
		if !hasFrontend {
			return false, fmt.Errorf("--include both pedido pero este plugin no tiene frontend/package.json")
		}
		return true, nil
	default:
		return false, fmt.Errorf("--include %q inválido — válidos: \"auto\", \"backend\", \"both\"", include)
	}
}

// copyPluginTree copia installed.Dir a outDir, excluyendo .git y
// node_modules siempre, frontend/ entero si !includeFrontend, y el venv
// de Python si isPython (ver el paquete doc comment: no es portable
// entre máquinas — el README generado explica cómo recrearlo).
func copyPluginTree(srcDir, dstDir string, includeFrontend, isPython bool) error {
	skipDirs := map[string]bool{".git": true, "node_modules": true}
	if isPython {
		skipDirs["venv"] = true
	}

	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		base := filepath.Base(path)
		if d.IsDir() {
			if skipDirs[base] {
				return filepath.SkipDir
			}
			if !includeFrontend && rel == "frontend" {
				return filepath.SkipDir
			}
		}

		target := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// Un symlink (común dentro de un venv, ej. bin/python3 -> el
		// intérprete del sistema) NO se sigue acá — copiarlo tal cual
		// evita arrastrar binarios del sistema del host al export. Como
		// isPython ya excluye venv/ entero arriba, en la práctica esto
		// solo aplica a symlinks fuera del venv, si el plugin tuviera
		// alguno.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		return copyFile(path, target, d)
	})
}

func copyFile(src, dst string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// writeBackendEnv arma exactamente las mismas env vars que Start() ya
// pasa al proceso del plugin (ver internal/plugins/process.go) — la
// diferencia es que acá se escriben a un archivo en vez de pasarse a un
// exec.Command en memoria. Formato .env plano (KEY=value, sin comillas)
// a propósito: es lo que docker run --env-file y la gran mayoría de
// parsers de dotenv esperan — bash 'source' también lo lee bien mientras
// los valores no tengan caracteres especiales de shell.
func writeBackendEnv(path string, installed plugins.Installed, config map[string]string, port int) error {
	var b strings.Builder
	b.WriteString("# Generado por 'asterion plugin export' — CONTIENE SECRETOS REALES.\n")
	b.WriteString("# Nunca lo commitees (el .gitignore de este export ya lo excluye). Si este\n")
	b.WriteString("# archivo se filtra, rotá cualquier secreto que tenga adentro.\n")
	b.WriteString("#\n")
	b.WriteString("# Formato .env estándar (KEY=value) — compatible con 'docker run --env-file',\n")
	b.WriteString("# docker-compose 'env_file:', y la mayoría de librerías de dotenv.\n\n")

	fmt.Fprintf(&b, "ASTERION_PLUGIN_NAME=%s\n", installed.Name)
	fmt.Fprintf(&b, "ASTERION_PLUGIN_PORT=%d\n", port)
	b.WriteString("ASTERION_PLUGIN_DIR=.\n")

	keys := make([]string, 0, len(config))
	for k := range config {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		value := config[k]
		if strings.ContainsAny(value, "\n\r") {
			return fmt.Errorf("el valor de config %q tiene un salto de línea — no se puede representar en formato .env de forma segura", k)
		}
		fmt.Fprintf(&b, "ASTERION_PLUGIN_CONFIG_%s=%s\n", strings.ToUpper(k), value)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// writeFrontendSafeEnv arma el .env que el BUILD del frontend puede ver —
// deliberadamente construido aparte de writeBackendEnv, con un subconjunto
// filtrado: solo los campos de config_schema que plugin.yaml marcó como
// NO secretos (mismo criterio que ya usa GetConfigMasked para decidir qué
// mostrarle a frontend-core). Un campo sin 'secret' declarado se trata
// como no-secreto (default false en el contrato) — si el plugin necesita
// que algo nunca llegue acá, tiene que marcarlo secret: true en su propio
// plugin.yaml, no hay otra señal disponible.
func writeFrontendSafeEnv(path string, installed plugins.Installed, config map[string]string) error {
	var b strings.Builder
	b.WriteString("# Generado por 'asterion plugin export' — SOLO valores NO secretos.\n")
	b.WriteString("# A propósito separado de .env_asterion_produced (el del backend, con los\n")
	b.WriteString("# secretos reales): el build del frontend nunca debe tener acceso a ese\n")
	b.WriteString("# archivo — cualquier variable que un bundler exponga termina siendo pública\n")
	b.WriteString("# en el bundle servido al navegador. Acá solo entran los campos de\n")
	b.WriteString("# config_schema marcados explícitamente 'secret: false' (o sin declarar).\n")
	b.WriteString("#\n")
	b.WriteString("# Nota: el nombre de estas variables sigue la convención de Asterion\n")
	b.WriteString("# (ASTERION_PLUGIN_CONFIG_*) — según el framework de tu frontend (Vite exige\n")
	b.WriteString("# prefijo VITE_, Next.js NEXT_PUBLIC_, etc.), puede hacer falta ajustar el\n")
	b.WriteString("# envPrefix de tu propio build config para que las levante — eso lo controla\n")
	b.WriteString("# el código del plugin, no Asterion.\n\n")

	var keys []string
	for _, f := range installed.Manifest.ConfigSchema {
		if !f.Secret {
			keys = append(keys, f.Key)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		value := config[k]
		if strings.ContainsAny(value, "\n\r") {
			continue
		}
		fmt.Fprintf(&b, "ASTERION_PLUGIN_CONFIG_%s=%s\n", strings.ToUpper(k), value)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
