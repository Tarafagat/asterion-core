package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	langparser "github.com/Tarafagat/asterion-language/parser"
	"github.com/Tarafagat/asterion-language/systemspec"

	"asterion-core/internal/plugins"
)

// pluginSystemExportCmd exporta CADA plugin declarado en un archivo de
// sistema a su propia subcarpeta (mismo formato que 'plugin export' para
// cada uno — ver plugin_export.go), más un .env_asterion_produced
// combinado a nivel sistema, prefijado por plugin, como referencia única
// para editar todo desde un solo lugar. El que de verdad usa cada
// servicio al correr sigue siendo el .env_asterion_produced DENTRO de su
// propia subcarpeta (sin prefijo, formato idéntico a un export puntual).
func pluginSystemExportCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "export <archivo.asterion>",
		Short: "Exporta cada plugin del sistema a su propia carpeta portable, más un .env combinado de referencia",
		Long: "Mismo export que 'plugin export' (ver ahí para el detalle de qué genera cada uno:\n" +
			".env_asterion_produced, frontend/.env.production filtrado, Dockerfile, README) pero\n" +
			"para cada plugin declarado en el archivo de sistema, cada uno en su propia\n" +
			"subcarpeta <out>/<nombre>/.\n\n" +
			"Los System.wire con field=\"port\" se resuelven contra puertos FIJOS asignados acá\n" +
			"(8080, 8081, ... en el orden del archivo) en vez del puerto efímero de desarrollo —\n" +
			"ese no va a existir en el destino. Se asume que los servicios se despliegan juntos,\n" +
			"en el mismo host — editá el .env_asterion_produced de nivel sistema a mano si el\n" +
			"destino real es otro (hosts separados, nombres de servicio de un docker-compose,\n" +
			"etc.).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginSystemExport(args[0], out)
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "Directorio de salida (default: ./<nombre del archivo>-export)")
	return cmd
}

func runPluginSystemExport(path, out string) error {
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
		return fmt.Errorf("%s no declaró ningún System.plugin(...) — no hay nada que exportar", path)
	}

	if out == "" {
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		out = "./" + base + "-export"
	}
	outAbs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outAbs, 0o755); err != nil {
		return err
	}

	baseDir := filepath.Dir(path)
	dslNameToReal := make(map[string]string, len(pluginDecls))
	dslNameToPort := make(map[string]int, len(pluginDecls))
	dslNameToConfig := make(map[string]map[string]string, len(pluginDecls))
	dslNameToFrontend := make(map[string]bool, len(pluginDecls))

	// Puertos fijos y secuenciales para el export — el puerto efímero de
	// desarrollo de cada plugin no significa nada fuera de esta máquina.
	nextPort := 8080

	for _, decl := range pluginDecls {
		realName, err := resolveOrInstall(decl, baseDir)
		if err != nil {
			return fmt.Errorf("%s: %w", decl.Name, err)
		}
		dslNameToReal[decl.Name] = realName

		port := nextPort
		nextPort++
		dslNameToPort[decl.Name] = port

		pluginOut := filepath.Join(outAbs, realName)
		fmt.Printf("--- %s → %q (puerto %d) ---\n", decl.Name, realName, port)
		includedFrontend, config, err := exportPluginTo(realName, pluginOut, "auto", port)
		if err != nil {
			return fmt.Errorf("%s: %w", decl.Name, err)
		}
		dslNameToFrontend[decl.Name] = includedFrontend
		dslNameToConfig[decl.Name] = config
	}

	// Los System.wire con field="port" se resuelven acá contra los
	// puertos FIJOS recién asignados — reescribe el .env_asterion_produced
	// de cada plugin dependiente para que apunte a esos, no al puerto de
	// desarrollo que exportPluginTo escribió por default (que asume que
	// cada plugin se exporta solo, sin sistema).
	for _, w := range wireDecls {
		targetReal := dslNameToReal[w.ToPlugin]
		value, err := resolveWireValueForExport(w, dslNameToReal, dslNameToPort, dslNameToConfig)
		if err != nil {
			return fmt.Errorf("wire hacia %q (clave %q): %w", w.ToPlugin, w.Key, err)
		}
		envPath := filepath.Join(outAbs, targetReal, ".env_asterion_produced")
		if err := appendOrReplaceEnvVar(envPath, "ASTERION_PLUGIN_CONFIG_"+strings.ToUpper(w.Key), value); err != nil {
			return fmt.Errorf("no pude actualizar %s con el wiring de %q: %w", envPath, w.Key, err)
		}
		// Si target tiene frontend Y la clave wireada es una de sus
		// config_schema NO secretas, su frontend/.env.production (ya
		// escrito por exportPluginTo con el snapshot DE ANTES del
		// wiring) queda desactualizado — se regenera acá, ahora que el
		// backend .env ya tiene el valor final. Sin esto, un valor
		// wireado explícitamente no-secreto (ej. una URL pública de API)
		// quedaría vacío en el .env del frontend en vez del real.
		if err := refreshFrontendSafeEnvAfterWiring(outAbs, targetReal); err != nil {
			return fmt.Errorf("no pude actualizar el .env del frontend de %q tras el wiring: %w", targetReal, err)
		}
	}

	if err := writeSystemCombinedEnv(outAbs, pluginDecls, dslNameToReal); err != nil {
		return err
	}

	fmt.Printf("\n✓ sistema exportado a %s (%d plugin(s), cada uno en su propia subcarpeta)\n", outAbs, len(pluginDecls))
	return nil
}

// resolveWireValueForExport es el equivalente, para export, de
// resolveWireValue (usado por 'system apply' contra el estado REAL en
// ejecución) — acá "port" resuelve contra el puerto FIJO recién asignado
// para el export, no contra un proceso corriendo.
func resolveWireValueForExport(
	w systemspec.WireDecl,
	dslNameToReal map[string]string,
	dslNameToPort map[string]int,
	dslNameToConfig map[string]map[string]string,
) (string, error) {
	if w.Field == "port" {
		port, ok := dslNameToPort[w.FromPlugin]
		if !ok {
			return "", fmt.Errorf("%q no tiene un puerto asignado en este export", w.FromPlugin)
		}
		return fmt.Sprintf("http://127.0.0.1:%d", port), nil
	}
	if key, ok := strings.CutPrefix(w.Field, "env:"); ok {
		cfg := dslNameToConfig[w.FromPlugin]
		value, ok := cfg[key]
		if !ok {
			return "", fmt.Errorf("%q no tiene configurada la clave %q", w.FromPlugin, key)
		}
		return value, nil
	}
	return "", fmt.Errorf("field %q desconocido — válidos: \"port\", \"env:<CLAVE>\"", w.Field)
}

// appendOrReplaceEnvVar reescribe (o agrega, si no estaba) una sola
// variable dentro de un .env ya generado por writeBackendEnv — evita
// reescribir todo el archivo de punta a punta solo para corregir el
// wiring después de exportar. Preserva permisos 0600.
func appendOrReplaceEnvVar(path, key, value string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	prefix := key + "="
	replaced := false
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = prefix + value
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, prefix+value)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// refreshFrontendSafeEnvAfterWiring regenera el frontend/.env.production
// de un plugin (si tiene uno) a partir de su .env_asterion_produced YA
// ACTUALIZADO por el wiring — no-op si no tiene frontend. Reusa
// writeFrontendSafeEnv (la misma lógica de filtrado por config_schema.Secret
// que ya usa un export puntual), reconstruyendo el mapa de config a
// partir de las claves ASTERION_PLUGIN_CONFIG_* del .env del backend en
// vez de volver a llamar plugins.GetConfig (el wiring nunca escribe ahí —
// solo toca el archivo exportado, nunca la config guardada del plugin).
func refreshFrontendSafeEnvAfterWiring(outAbs, realName string) error {
	frontendEnvPath := filepath.Join(outAbs, realName, "frontend", ".env.production")
	if _, err := os.Stat(frontendEnvPath); err != nil {
		return nil
	}
	installed, err := plugins.Get(realName)
	if err != nil {
		return err
	}
	backendVars, err := readEnvFile(filepath.Join(outAbs, realName, ".env_asterion_produced"))
	if err != nil {
		return err
	}
	config := map[string]string{}
	for k, v := range backendVars {
		if bareKey, ok := strings.CutPrefix(k, "ASTERION_PLUGIN_CONFIG_"); ok {
			config[strings.ToLower(bareKey)] = v
		}
	}
	return writeFrontendSafeEnv(frontendEnvPath, installed, config)
}

// writeSystemCombinedEnv arma el .env_asterion_produced de nivel sistema
// — la referencia única para editar todo desde un solo lugar (pedida
// explícitamente): junta las variables de TODOS los plugins, prefijadas
// por su nombre de variable del .asterion en mayúsculas, para que no
// choquen entre sí. Es puramente de referencia — el que de verdad usa
// cada servicio al correr es el .env_asterion_produced de su propia
// subcarpeta (sin prefijo).
//
// Lee el .env_asterion_produced YA TERMINADO de cada subcarpeta (después
// de que el wiring ya lo actualizó — ver appendOrReplaceEnvVar más
// arriba) en vez de reusar el snapshot de config que exportPluginTo tomó
// ANTES de resolver el wiring: si leyera ese snapshot viejo, la
// referencia combinada nunca mostraría valores como DATABASE_URL que
// System.wire agrega recién después — quedaría desincronizada del
// archivo que de verdad importa apenas se termina de generar.
func writeSystemCombinedEnv(outAbs string, pluginDecls []systemspec.PluginDecl, dslNameToReal map[string]string) error {
	var b strings.Builder
	b.WriteString("# Generado por 'asterion plugin system export' — referencia COMBINADA de\n")
	b.WriteString("# todos los plugins de este sistema. CONTIENE SECRETOS REALES de todos ellos.\n")
	b.WriteString("#\n")
	b.WriteString("# Este archivo es solo de referencia/edición — cada servicio, al correr, usa\n")
	b.WriteString("# el .env_asterion_produced DENTRO de su propia subcarpeta (<nombre>/), no\n")
	b.WriteString("# este. Si editás un valor acá, actualizá también el de la subcarpeta\n")
	b.WriteString("# correspondiente — este archivo no se vuelve a leer solo.\n")
	b.WriteString("#\n")
	b.WriteString("# Los puertos de abajo son FIJOS, asignados en este export (8080, 8081, ...) —\n")
	b.WriteString("# asumen que todos los servicios corren juntos, en el mismo host. Si el\n")
	b.WriteString("# destino real es otro (hosts separados, nombres de servicio de un\n")
	b.WriteString("# docker-compose), editá los .env_asterion_produced de cada subcarpeta a mano.\n\n")

	for _, decl := range pluginDecls {
		realName := dslNameToReal[decl.Name]
		prefix := strings.ToUpper(decl.Name) + "__"

		pluginEnvPath := filepath.Join(outAbs, realName, ".env_asterion_produced")
		vars, err := readEnvFile(pluginEnvPath)
		if err != nil {
			return fmt.Errorf("no pude releer %s para armar la referencia combinada: %w", pluginEnvPath, err)
		}

		fmt.Fprintf(&b, "# --- %s (%s, ./%s/) ---\n", decl.Name, realName, realName)
		keys := make([]string, 0, len(vars))
		for k := range vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s%s=%s\n", prefix, k, vars[k])
		}
		b.WriteString("\n")
	}

	path := filepath.Join(outAbs, ".env_asterion_produced")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	fmt.Printf("✓ %s — referencia combinada de todos los plugins (permisos 0600, nunca lo commitees)\n", path)
	return nil
}

// readEnvFile parsea un .env plano (KEY=value, un comentario '#' arranca
// la línea, líneas vacías se ignoran) — mismo formato simple que
// writeBackendEnv ya escribe, así que alcanza con este parser chico en
// vez de traer una librería de dotenv para esto.
func readEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[key] = value
	}
	return out, nil
}
