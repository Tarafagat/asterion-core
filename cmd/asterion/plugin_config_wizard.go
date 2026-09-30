package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/Tarafagat/asterion-plugin-contract/apc"

	"asterion-core/internal/plugins"
)

// configWizard es el menú interactivo de 'asterion plugin config set'
// cuando se lo invoca SIN pares clave=valor. Existe porque un plugin real
// puede declarar 20+ campos (visto en vivo con fuelity_bot: JWT, dos
// bases de datos, Redis, Ollama, SMTP, CORS) y configurarlos uno por uno
// tipeando el nombre exacto de cada clave es lento y fácil de errar.
//
// La forma no interactiva NUNCA cambia: 'config set <plugin> k=v k2=v2'
// sigue funcionando igual, que es lo que sirve para scripts y CI. El
// menú es un atajo para humanos, no un reemplazo — y al terminar imprime
// el comando manual equivalente, para que quede a mano cómo repetirlo
// sin el menú (en otra máquina, en un script, en un README).
func runConfigWizard(name string) error {
	installed, err := plugins.Get(name)
	if err != nil {
		return err
	}
	schema := installed.Manifest.ConfigSchema
	if len(schema) == 0 {
		fmt.Printf("%q no declara ningún config_schema — no hay nada que configurar.\n", installed.Name)
		return nil
	}

	// Sin terminal interactiva (pipe, CI, cron) el menú no tiene sentido
	// y colgarse esperando una línea que nunca llega sería peor: se
	// explica la forma manual y se sale.
	if !stdinIsTerminal() {
		printManualHelp(installed.Name, schema, nil)
		return fmt.Errorf("no hay una terminal interactiva — usá la forma con clave=valor de arriba")
	}

	current, err := plugins.GetConfigMasked(installed)
	if err != nil {
		return err
	}

	// pending junta lo editado en esta sesión sin guardar nada todavía:
	// se escribe una sola vez al final (y solo si el usuario confirma),
	// así salir con 'q' de verdad no deja nada a medio configurar.
	pending := map[string]string{}

	fmt.Printf("\nConfigurando %q — %d campo(s)\n", installed.Name, len(schema))
	fmt.Println("Elegí un número para editar ese campo. Otras opciones:")
	fmt.Println("  f  completar solo los OBLIGATORIOS que falten, uno tras otro")
	fmt.Println("  t  recorrer TODOS los campos, uno tras otro")
	fmt.Println("  g  guardar y salir     q  salir sin guardar")

	for {
		printConfigMenu(schema, current, pending)

		fmt.Print("\n> ")
		choice := strings.TrimSpace(trimNewline(readLine()))

		switch strings.ToLower(choice) {
		case "":
			continue

		case "q":
			if len(pending) > 0 {
				fmt.Printf("Descartando %d cambio(s) sin guardar.\n", len(pending))
			}
			return nil

		case "g":
			if len(pending) == 0 {
				fmt.Println("No hay cambios para guardar.")
				return nil
			}
			if err := plugins.SetConfig(installed.Name, pending); err != nil {
				return err
			}
			fmt.Printf("\n✓ Config de %q actualizada (%d campo(s), cifrados en disco)\n", installed.Name, len(pending))
			printManualHelp(installed.Name, schema, pending)
			printNextStep(installed, schema, current, pending)
			return nil

		case "f":
			askEach(schema, current, pending, true)

		case "t":
			askEach(schema, current, pending, false)

		default:
			n, err := strconv.Atoi(choice)
			if err != nil || n < 1 || n > len(schema) {
				fmt.Printf("No entendí %q — poné un número entre 1 y %d, o f/t/g/q.\n", choice, len(schema))
				continue
			}
			askField(schema[n-1], current, pending)
		}
	}
}

// printConfigMenu imprime la lista numerada con el estado real de cada
// campo: qué ya está guardado, qué se acaba de editar sin guardar, y qué
// falta. Un secreto nunca se muestra en claro — ni el que ya estaba
// (viene enmascarado de GetConfigMasked) ni el recién tipeado.
func printConfigMenu(schema []apc.ConfigField, current, pending map[string]string) {
	fmt.Println()
	for i, f := range schema {
		marker, value := fieldState(f, current, pending)

		label := f.Label
		if label == "" {
			label = f.Key
		}
		req := ""
		if f.Required {
			req = " (obligatorio)"
		}

		fmt.Printf(" %s [%2d] %-24s %s%s\n", marker, i+1, f.Key, label, req)
		if value != "" {
			fmt.Printf("           %s\n", value)
		}
	}
}

// fieldState decide con qué marcador y qué valor se muestra un campo:
//
//	✓ ya guardado de antes    * editado ahora, sin guardar
//	✗ obligatorio y falta     · opcional y vacío
func fieldState(f apc.ConfigField, current, pending map[string]string) (marker, value string) {
	if v, edited := pending[f.Key]; edited {
		if f.Secret {
			return "*", "(secreto, sin guardar)"
		}
		return "*", v + "  (sin guardar)"
	}
	if v, ok := current[f.Key]; ok && v != "" {
		return "✓", v
	}
	if f.Required {
		return "✗", ""
	}
	if f.Default != "" {
		return "·", "default: " + f.Default
	}
	return "·", ""
}

// askEach recorre los campos de corrido. onlyMissingRequired es el atajo
// más útil en la práctica: completar lo mínimo para que el plugin pueda
// arrancar, sin pasar por los 15 opcionales.
func askEach(schema []apc.ConfigField, current, pending map[string]string, onlyMissingRequired bool) {
	for _, f := range schema {
		if onlyMissingRequired {
			if !f.Required {
				continue
			}
			if _, edited := pending[f.Key]; edited {
				continue
			}
			if v, ok := current[f.Key]; ok && v != "" {
				continue
			}
		}
		if !askField(f, current, pending) {
			fmt.Println("(corte — volviendo al menú)")
			return
		}
	}
}

// askField pide UN valor. Enter sin escribir nada deja el campo como
// estaba — nunca borra un valor ya guardado por accidente. Devuelve
// false si el usuario cortó con ':q' para volver al menú.
func askField(f apc.ConfigField, current, pending map[string]string) bool {
	label := f.Label
	if label == "" {
		label = f.Key
	}
	fmt.Printf("\n%s\n", label)

	switch {
	case f.Secret:
		fmt.Println("  (secreto: no se va a ver mientras lo escribís, y se guarda cifrado)")
	case f.Default != "":
		fmt.Printf("  (default si lo dejás vacío: %s)\n", f.Default)
	}
	if v, ok := current[f.Key]; ok && v != "" {
		fmt.Printf("  (actual: %s)\n", v)
	}
	fmt.Printf("  %s = ", f.Key)

	var value string
	if f.Secret {
		value = readSecretLine()
	} else {
		value = strings.TrimSpace(trimNewline(readLine()))
	}

	if value == ":q" {
		return false
	}
	if value == "" {
		fmt.Println("  (sin cambios)")
		return true
	}
	pending[f.Key] = value
	if f.Secret {
		fmt.Println("  ✓ anotado (secreto)")
	} else {
		fmt.Printf("  ✓ anotado: %s\n", value)
	}
	return true
}

// printManualHelp explica la forma NO interactiva equivalente — que es
// la que sirve para scripts, para documentar en un README, o para
// repetir la misma config en otra máquina. Con `configured` no vacío
// muestra exactamente el comando que reproduce lo que se acaba de
// guardar; sin él, la forma general.
//
// Los valores de campos secretos NUNCA se imprimen: van como
// <...> para que el comando sirva de plantilla sin filtrar nada al
// historial del shell ni al scrollback de la terminal.
func printManualHelp(pluginName string, schema []apc.ConfigField, configured map[string]string) {
	secret := map[string]bool{}
	required := map[string]bool{}
	for _, f := range schema {
		secret[f.Key] = f.Secret
		required[f.Key] = f.Required
	}

	fmt.Println("\nSin el menú (para scripts, CI, o repetirlo en otra máquina):")

	if len(configured) > 0 {
		keys := make([]string, 0, len(configured))
		for k := range configured {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var parts []string
		var hidSecret bool
		for _, k := range keys {
			if secret[k] {
				parts = append(parts, fmt.Sprintf("%s=<%s>", k, k))
				hidSecret = true
				continue
			}
			parts = append(parts, fmt.Sprintf("%s=%s", k, shellQuote(configured[k])))
		}
		fmt.Printf("  asterion plugin config set %s \\\n    %s\n", pluginName, strings.Join(parts, " \\\n    "))
		if hidSecret {
			fmt.Println("\n  Los valores secretos van como <clave> a propósito — no se imprimen para")
			fmt.Println("  que este comando pueda pegarse en un README sin filtrar nada.")
		}
		return
	}

	var example []string
	for _, f := range schema {
		if f.Required {
			ph := f.Key
			example = append(example, fmt.Sprintf("%s=<%s>", f.Key, ph))
		}
		if len(example) == 3 {
			break
		}
	}
	if len(example) == 0 {
		example = []string{"clave=valor"}
	}
	fmt.Printf("  asterion plugin config set %s %s ...\n", pluginName, strings.Join(example, " "))
	fmt.Printf("  asterion plugin config show %s          # ver qué quedó guardado (secretos enmascarados)\n", pluginName)
}

// printNextStep cierra diciendo qué falta para poder arrancar — la
// pregunta real de quien acaba de configurar algo.
func printNextStep(installed plugins.Installed, schema []apc.ConfigField, current, pending map[string]string) {
	var missing []string
	for _, f := range schema {
		if !f.Required {
			continue
		}
		if _, edited := pending[f.Key]; edited {
			continue
		}
		if v, ok := current[f.Key]; ok && v != "" {
			continue
		}
		missing = append(missing, f.Key)
	}

	fmt.Println()
	if len(missing) > 0 {
		fmt.Printf("Todavía faltan %d campo(s) obligatorio(s): %s\n", len(missing), strings.Join(missing, ", "))
		fmt.Printf("Volvé a entrar con: asterion plugin config set %s\n", installed.Name)
		return
	}
	fmt.Printf("Ya están todos los obligatorios. Arrancalo con: asterion plugin start %s\n", installed.Name)
}

// shellQuote entrecomilla un valor solo si lo necesita — un comando que
// se va a copiar y pegar tiene que ser correcto aunque el valor tenga
// espacios, comillas o un '$'.
func shellQuote(v string) string {
	if v != "" && !strings.ContainsAny(v, " \t\n\"'$`\\|&;<>()*?[]#~") {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// stdinIsTerminal responde si stdin es una terminal de verdad. Sin
// dependencias nuevas: en un pipe o una redirección, os.Stdin.Stat()
// reporta ModeCharDevice apagado.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// readSecretLine lee una línea sin que se vea mientras se tipea, usando
// 'stty' — el mismo mecanismo que usa cualquier prompt de contraseña de
// Unix, sin agregar ninguna dependencia al módulo (traer golang.org/x/term
// arrastraba además una subida del go directive del proyecto).
//
// En Windows, o si 'stty' no está disponible, NO se finge: se avisa que
// el valor se va a ver mientras se escribe y se lee normal — mejor eso
// que dar una sensación falsa de privacidad. Mismo criterio que el resto
// del CLI con las limitaciones por plataforma (ver 'plugin status' en
// Windows).
func readSecretLine() string {
	restore, ok := disableEcho()
	if !ok {
		fmt.Print("\n  ⚠ (no pude apagar el eco en esta terminal: el valor SE VA A VER mientras lo escribís)\n  = ")
		return strings.TrimSpace(trimNewline(readLine()))
	}
	defer restore()

	value := strings.TrimSpace(trimNewline(readLine()))
	fmt.Println() // el Enter del usuario no se imprimió solo, con el eco apagado
	return value
}

// disableEcho apaga el eco de la terminal y devuelve cómo restaurarlo.
func disableEcho() (restore func(), ok bool) {
	if runtime.GOOS == "windows" {
		return nil, false
	}
	if _, err := exec.LookPath("stty"); err != nil {
		return nil, false
	}
	if err := sttyRun("-echo"); err != nil {
		return nil, false
	}
	return func() { _ = sttyRun("echo") }, true
}

func sttyRun(arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin // stty actúa sobre ESTA terminal, no sobre la suya
	return cmd.Run()
}
