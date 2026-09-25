package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	langparser "github.com/Tarafagat/asterion-language/parser"
	"github.com/Tarafagat/asterion-language/systemspec"

	"asterion-core/internal/plugins"
)

// pluginSystemCmd agrupa los comandos que declaran/levantan un SISTEMA de
// varios plugins interconectados desde un único .asterion — distinto de
// 'plugin from-asterion' (que describe UN plugin, su propio plugin.yaml).
// Ver systemspec (asterion-language) para el DSL (System.plugin/
// System.wire) y el plan en curso para el diseño completo.
func pluginSystemCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "system",
		Short: "Declara y levanta un sistema de varios plugins interconectados desde un .asterion",
	}
	root.AddCommand(
		pluginSystemApplyCmd(),
		pluginSystemWatchCmd(),
		pluginSystemWatchInstallCmd(),
		pluginSystemWatchUninstallCmd(),
		pluginSystemExportCmd(),
	)
	return root
}

func pluginSystemApplyCmd() *cobra.Command {
	var build bool
	cmd := &cobra.Command{
		Use:   "apply <archivo.asterion>",
		Short: "Instala, conecta y arranca todos los plugins declarados en un archivo de sistema",
		Long: "Compila un archivo con System.plugin(...)/System.wire(...) (ver Asterion Language) e\n" +
			"instala cada plugin declarado (carpeta local con --link, o clona si route es una URL de\n" +
			"git), marca el que declaró principal=true, resuelve cada System.wire contra el estado\n" +
			"REAL del plugin origen (su puerto real, o una config ya guardada con field=\"env:<CLAVE>\")\n" +
			"y arranca todo en el orden en que aparece en el archivo — el propio lenguaje no permite\n" +
			"referencias hacia adelante, así que ese orden ya es el de instalación/arranque correcto.\n\n" +
			"El archivo nunca puede contener un secreto de verdad: System.wire solo declara QUÉ\n" +
			"clave copiar de dónde, nunca un valor — los secretos de cada plugin se siguen\n" +
			"configurando aparte, con 'asterion plugin config set' de siempre.\n\n" +
			"Correrlo de nuevo sobre el mismo archivo es seguro (no reinstala lo que ya está, no\n" +
			"rearranca lo que no cambió) — es también la forma de 'reactivar' el sistema si el\n" +
			"puerto de un plugin cambió (ver 'asterion plugin system watch' para que esto pase solo).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return applySystemFile(args[0], build)
		},
	}
	cmd.Flags().BoolVar(&build, "build", false, "Compilar cada plugin (backend + frontend) antes de arrancarlo")
	return cmd
}

// applySystemFile es la reconciliación completa — parsear, compilar,
// instalar/actualizar, arrancar. La reusa también 'plugin system watch'
// (§7 del plan, todavía no implementado en este archivo) en un loop.
func applySystemFile(path string, build bool) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no pude leer %s: %w", path, err)
	}

	prog, parseDiags := langparser.Parse(src, path)
	if parseDiags.HasErrors() {
		fmt.Print(parseDiags.String())
		return fmt.Errorf("%s no compila", path)
	}

	pluginDecls, wireDecls, compileDiags := systemspec.Compile(prog)
	if compileDiags.HasErrors() {
		fmt.Print(compileDiags.String())
		return fmt.Errorf("%s no se pudo compilar a un sistema de plugins", path)
	}
	if len(pluginDecls) == 0 {
		return fmt.Errorf("%s no declaró ningún System.plugin(...) — no hay nada que aplicar", path)
	}

	// dslNameToReal traduce el nombre de variable del .asterion (db, api,
	// ...) al nombre REAL del plugin instalado (el que declara su propio
	// plugin.yaml) — System.wire referencia lo primero, pero
	// plugins.Get/SetConfig/Start necesitan lo segundo.
	dslNameToReal := make(map[string]string, len(pluginDecls))

	baseDir := filepath.Dir(path)
	for _, decl := range pluginDecls {
		realName, err := resolveOrInstall(decl, baseDir)
		if err != nil {
			return fmt.Errorf("%s: %w", decl.Name, err)
		}
		dslNameToReal[decl.Name] = realName
		fmt.Printf("✓ %s → %q\n", decl.Name, realName)

		if decl.Principal {
			if err := plugins.SetMain(realName); err != nil {
				return fmt.Errorf("no pude marcar %q como principal: %w", realName, err)
			}
		}
	}

	wiresByTarget := make(map[string][]systemspec.WireDecl, len(wireDecls))
	for _, w := range wireDecls {
		wiresByTarget[w.ToPlugin] = append(wiresByTarget[w.ToPlugin], w)
	}

	for _, decl := range pluginDecls {
		realName := dslNameToReal[decl.Name]

		changed, err := applyWires(realName, wiresByTarget[decl.Name], dslNameToReal)
		if err != nil {
			return fmt.Errorf("%s: %w", decl.Name, err)
		}

		if err := buildAndStartPlugin(decl, realName, changed, build); err != nil {
			return fmt.Errorf("%s: %w", decl.Name, err)
		}
	}
	return nil
}

// buildAndStartPlugin hace el build (opcional) + start/restart de UN
// plugin — separado del loop de applySystemFile en su propia función
// para que el PATH con el toolchain de este plugin (ensureToolchainsOnPath)
// quede scoped SOLO a esta llamada vía defer: un defer adentro de un for
// no se ejecuta hasta que la función que lo contiene termina, así que
// varios plugins con distintos 'requires' se pisarían entre sí si esto
// viviera directo en el loop.
func buildAndStartPlugin(decl systemspec.PluginDecl, realName string, wiringChanged, build bool) error {
	if len(decl.Requires) > 0 {
		restorePath, err := ensureToolchainsOnPath(decl.Requires)
		if err != nil {
			return err
		}
		defer restorePath()
	}

	if build {
		fmt.Printf("Compilando %q...\n", realName)
		log, err := plugins.Build(realName)
		if log != "" {
			fmt.Println(log)
		}
		if err != nil {
			return err
		}
	}

	status, err := plugins.Status(realName)
	if err != nil {
		return err
	}

	switch {
	case status.Status != "running":
		installed, err := plugins.Start(realName)
		if err != nil {
			return fmt.Errorf("no pude arrancarlo: %w", err)
		}
		fmt.Printf("✓ %q corriendo — puerto %d, pid %d\n", realName, installed.Port, installed.PID)
	case wiringChanged:
		installed, err := plugins.Restart(realName)
		if err != nil {
			return fmt.Errorf("no pude reiniciarlo tras actualizar su wiring: %w", err)
		}
		fmt.Printf("✓ %q reiniciado (wiring actualizado) — puerto %d, pid %d\n", realName, installed.Port, installed.PID)
	default:
		fmt.Printf("= %q ya está corriendo, sin cambios de wiring\n", realName)
	}
	return nil
}

// resolveOrInstall decide de dónde sale este plugin (carpeta local vs.
// git) y lo instala — salvo que ya esté instalado, en cuyo caso no hace
// nada (idempotente: re-aplicar el mismo archivo no reinstala). route
// relativa se resuelve contra baseDir (el directorio del propio
// .asterion), no contra el cwd de quien corre el comando.
func resolveOrInstall(decl systemspec.PluginDecl, baseDir string) (string, error) {
	localPath := decl.Route
	if !filepath.IsAbs(localPath) {
		localPath = filepath.Join(baseDir, decl.Route)
	}
	info, statErr := os.Stat(localPath)
	isLocal := statErr == nil && info.IsDir()

	var expectedName string
	if isLocal {
		manifest, err := plugins.LoadManifest(localPath)
		if err != nil {
			return "", fmt.Errorf("no pude leer plugin.yaml en %s: %w", localPath, err)
		}
		expectedName = manifest.Name
	} else {
		expectedName = plugins.DeriveName(decl.Route)
	}

	if existing, err := plugins.Get(expectedName); err == nil {
		return existing.Name, nil
	}

	if isLocal {
		installed, err := plugins.InstallLinked(localPath, "")
		if err != nil {
			return "", err
		}
		return installed.Name, nil
	}

	var installed plugins.Installed
	var err error
	if decl.Ref != "" {
		installed, err = plugins.InstallRef(decl.Route, "", decl.Ref)
	} else {
		installed, err = plugins.Install(decl.Route, "", false)
	}
	if err != nil {
		return "", err
	}
	return installed.Name, nil
}

// applyWires resuelve cada WireDecl que apunte a target contra el estado
// ACTUAL de su plugin origen y actualiza la config de target si hace
// falta — devuelve true si algo cambió (para que el caller decida si
// hace falta un restart). Nunca escribe un valor que no cambió: evita
// reiniciar plugins cuyo wiring sigue siendo el mismo que la corrida
// anterior.
func applyWires(target string, wires []systemspec.WireDecl, dslNameToReal map[string]string) (changed bool, err error) {
	if len(wires) == 0 {
		return false, nil
	}
	current, err := plugins.GetConfig(target)
	if err != nil {
		return false, err
	}
	updates := map[string]string{}
	for _, w := range wires {
		fromReal := dslNameToReal[w.FromPlugin]
		value, err := resolveWireValue(fromReal, w.Field)
		if err != nil {
			return false, fmt.Errorf("wire hacia %q (clave %q): %w", target, w.Key, err)
		}
		if current[w.Key] != value {
			updates[w.Key] = value
		}
	}
	if len(updates) == 0 {
		return false, nil
	}
	if err := plugins.SetConfig(target, updates); err != nil {
		return false, err
	}
	return true, nil
}

// resolveWireValue lee el campo pedido del plugin origen — "port" (su
// puerto real, tiene que estar corriendo) o "env:<CLAVE>" (una config ya
// guardada, vía el mismo canal cifrado de siempre — nunca un valor que
// haya viajado por el .asterion).
func resolveWireValue(pluginName, field string) (string, error) {
	if field == "port" {
		status, err := plugins.Status(pluginName)
		if err != nil {
			return "", err
		}
		if status.Port == 0 {
			return "", fmt.Errorf("%q todavía no tiene un puerto asignado — tiene que arrancar antes que lo que depende de él", pluginName)
		}
		return fmt.Sprintf("http://127.0.0.1:%d", status.Port), nil
	}
	if key, ok := strings.CutPrefix(field, "env:"); ok {
		cfg, err := plugins.GetConfig(pluginName)
		if err != nil {
			return "", err
		}
		value, ok := cfg[key]
		if !ok {
			return "", fmt.Errorf("%q no tiene configurada la clave %q — 'asterion plugin config set %s %s=...' primero", pluginName, key, pluginName, key)
		}
		return value, nil
	}
	return "", fmt.Errorf("field %q desconocido — válidos: \"port\", \"env:<CLAVE>\"", field)
}

// ensureToolchainsOnPath resuelve cada entrada de requires (hoy solo
// "node@<version>" — pnpm viene con Node vía corepack, no se declara
// aparte) y antepone su bin/ al PATH del proceso ACTUAL de asterion —
// nunca al PATH real del sistema — así que Build()/Start() (que arman su
// exec.Command a partir de os.Environ()) lo heredan para ESTE plugin
// puntual. El caller tiene que restaurar el PATH original (la función
// devuelta) apenas termine con este plugin — ver buildAndStartPlugin,
// donde eso pasa vía defer con scope de una sola iteración, nunca de
// todo el loop.
// unsandboxableTools son lenguajes/herramientas que requires=[...] no
// puede instalar de forma sandboxed hoy — a diferencia de Node (que
// publica un tarball portable oficial por versión exacta, verificado con
// checksum, ver EnsureNode), estos no tienen un equivalente limpio:
// Python no publica un build portable oficial (los "portable Python"
// que existen son proyectos de terceros, no del propio python.org); un
// compilador de C depende de headers/libs del sistema operativo, no es
// algo que se pueda bajar como un tarball autocontenido; Java tiene más
// de un vendor de JDK sin un default obvio. Pedirlos en requires=[...]
// da un error claro en vez de fingir que se resolvieron.
var unsandboxableTools = map[string]string{
	"java":  "no hay un vendor de JDK obvio para descargar sandboxed sin pedirte elegir uno — instalalo vos y declará solo \"java\" para que Asterion VERIFIQUE que está (no lo descarga)",
	"c":     "un compilador de C depende de headers/libs del sistema operativo — no es algo que se pueda bajar como tarball autocontenido",
	"gcc":   "un compilador de C depende de headers/libs del sistema operativo — no es algo que se pueda bajar como tarball autocontenido",
	"clang": "un compilador de C depende de headers/libs del sistema operativo — no es algo que se pueda bajar como tarball autocontenido",
}

// ensureToolchainsOnPath resuelve cada entrada de requires y antepone su
// bin/ al PATH del proceso ACTUAL de asterion (nunca al PATH real del
// sistema) — así Build()/Start() (que arman su exec.Command a partir de
// os.Environ()) lo heredan para ESTE plugin puntual. Dos formas de
// entrada, cada una con una garantía distinta y HONESTA sobre qué hace:
//
//   - "node@<version>" — SE INSTALA sandboxed de verdad si no estaba (ver
//     EnsureNode: descarga + verifica checksum + extrae). pnpm viene con
//     Node vía corepack, no se declara aparte.
//   - "python" / "go" (sin versión, a propósito — ver unsandboxableTools
//     para por qué Python no tiene el mismo tratamiento que Node) — SE
//     VERIFICA que ya está en el PATH del sistema, error claro si no.
//     Nunca se descarga nada para estos dos.
//
// Cualquier otra cosa (java, c, gcc, clang, ...) es un error explícito
// con el motivo — nunca un intento silencioso que podría fallar más
// adelante de forma confusa.
func ensureToolchainsOnPath(requires []string) (restore func(), err error) {
	var prefixes []string
	for _, r := range requires {
		if name, version, ok := strings.Cut(r, "@"); ok {
			if name != "node" || version == "" {
				return nil, fmt.Errorf(
					"requires %q no reconocido — la única forma con versión soportada es \"node@<version>\" (ej. \"node@20.11.0\")",
					r,
				)
			}
			binDir, err := plugins.EnsureNode(version)
			if err != nil {
				return nil, fmt.Errorf("requires %q: %w", r, err)
			}
			if err := plugins.EnsurePnpm(binDir); err != nil {
				return nil, fmt.Errorf("requires %q: no pude habilitar pnpm vía corepack: %w", r, err)
			}
			prefixes = append(prefixes, binDir)
			continue
		}

		switch r {
		case "python":
			if _, err := plugins.PythonInterpreter(); err != nil {
				return nil, fmt.Errorf("requires %q: %w", r, err)
			}
		case "go":
			if _, err := exec.LookPath("go"); err != nil {
				return nil, fmt.Errorf("requires %q: no encontré 'go' en el PATH", r)
			}
		default:
			if reason, known := unsandboxableTools[r]; known {
				return nil, fmt.Errorf("requires %q: Asterion todavía no sabe instalar esto — %s", r, reason)
			}
			return nil, fmt.Errorf(
				"requires %q no reconocido — válidos hoy: \"node@<version>\" (se instala sandboxed), \"python\"/\"go\" (se verifica que ya estén, no se instalan)",
				r,
			)
		}
	}
	if len(prefixes) == 0 {
		return func() {}, nil
	}

	original := os.Getenv("PATH")
	newPath := strings.Join(prefixes, string(os.PathListSeparator)) + string(os.PathListSeparator) + original
	os.Setenv("PATH", newPath)
	return func() { os.Setenv("PATH", original) }, nil
}
