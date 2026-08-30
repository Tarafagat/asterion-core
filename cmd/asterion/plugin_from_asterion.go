package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	langparser "github.com/Tarafagat/asterion-language/parser"
	"github.com/Tarafagat/asterion-language/pluginmanifest"

	"asterion-core/internal/plugins"
)

// pluginFromAsterionCmd es la alternativa sin heurística a 'plugin
// from-openapi': en vez de inferir resources/actions adivinando por la
// forma de una URL (y a veces adivinando mal), el autor declara cada
// campo del contrato explícito en un .asterion con llamadas
// Contract.<verbo>(...) — ver el DSL en
// asterion-language/spec/grammar.md § "DSL de manifiesto de plugin", y el
// compilador en asterion-language/pluginmanifest (repo hermano, no
// reimplementado acá).
func pluginFromAsterionCmd() *cobra.Command {
	var out string
	var force bool
	cmd := &cobra.Command{
		Use:   "from-asterion <archivo.asterion>",
		Short: "Compila un archivo Contract.*(...) de Asterion Language a un plugin.yaml, y lo valida",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginFromAsterion(args[0], out, force)
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "Directorio de salida (default: el 'name' declarado en Contract.define)")
	cmd.Flags().BoolVar(&force, "force", false, "Sobrescribir un plugin.yaml existente (el .asterion es la fuente editable — recompilar es el flujo normal)")
	return cmd
}

func runPluginFromAsterion(asterionPath, out string, force bool) error {
	src, err := os.ReadFile(asterionPath)
	if err != nil {
		return fmt.Errorf("no pude leer %s: %w", asterionPath, err)
	}

	prog, parseDiags := langparser.Parse(src, asterionPath)
	if parseDiags.HasErrors() {
		fmt.Print(parseDiags.String())
		return fmt.Errorf("%s no compila", asterionPath)
	}

	manifest, compileDiags := pluginmanifest.Compile(prog)
	if compileDiags.HasErrors() {
		fmt.Print(compileDiags.String())
		return fmt.Errorf("%s no se pudo compilar a un plugin.yaml", asterionPath)
	}

	outDir := out
	if outDir == "" {
		outDir = manifest.Name
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	outPath := filepath.Join(outDir, "plugin.yaml")
	if _, err := os.Stat(outPath); err == nil && !force {
		return fmt.Errorf("%s ya existe — pasá --force para sobrescribirlo (el .asterion es la fuente, recompilar es el flujo normal)", outPath)
	}

	data, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("✓ %s generado a partir de %s\n", outPath, asterionPath)
	fmt.Printf("  %d config field(s), %d resource(s), %d action(s)\n", len(manifest.ConfigSchema), len(manifest.Resources), len(manifest.Actions))

	// A diferencia de 'from-openapi' (heurístico, pensado para revisión
	// manual antes de confiar en él), acá cada campo vino declarado
	// explícito por el autor — tiene sentido validar en el momento en vez
	// de dejarlo para un paso aparte.
	if _, err := plugins.ValidateManifestDir(outDir); err != nil {
		return fmt.Errorf("el plugin.yaml generado no cumple el Asterion Plugin Contract: %w", err)
	}
	fmt.Printf("✓ %s cumple el Asterion Plugin Contract\n", outDir)
	return nil
}
