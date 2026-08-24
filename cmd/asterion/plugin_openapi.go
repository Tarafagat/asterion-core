package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Tarafagat/asterion-plugin-contract/openapi"
)

// pluginFromOpenAPICmd es el transformador API→plugin: si ya tenés una API
// REST propia con su OpenAPI, esto genera un plugin.yaml de partida
// agrupando paths en resources (patrones CRUD) y actions (todo lo demás) —
// ver asterion-plugin-contract/openapi para el criterio exacto. Heurístico
// a propósito: ahorra el primer borrador, no reemplaza el criterio de
// quien conoce la API de verdad.
func pluginFromOpenAPICmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "from-openapi <openapi.yaml>",
		Short: "Genera un plugin.yaml de partida infiriendo resources/actions de una API OpenAPI existente",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, err := openapi.Infer(args[0])
			if err != nil {
				return err
			}
			outDir := out
			if outDir == "" {
				outDir = manifest.Name
			}
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return err
			}
			outPath := filepath.Join(outDir, "plugin.yaml")
			if _, err := os.Stat(outPath); err == nil {
				return fmt.Errorf("%s ya existe — no lo piso", outPath)
			}
			data, err := yaml.Marshal(manifest)
			if err != nil {
				return err
			}
			if err := os.WriteFile(outPath, data, 0o644); err != nil {
				return err
			}
			fmt.Printf("✓ %s generado a partir de %s\n", outPath, args[0])
			fmt.Printf("  %d resource(s), %d action(s) inferidos\n", len(manifest.Resources), len(manifest.Actions))
			fmt.Println("\nEs un punto de partida, no un manifest terminado — revisá especialmente:")
			fmt.Println("  - start.command (asume un binario ./<nombre>, ajustalo a como arranca tu API de verdad)")
			fmt.Println("  - config_schema, permissions (nada de esto se puede inferir de un OpenAPI)")
			fmt.Println("  - resources[].schema, primary_key (si tu API no usa 'id', corregilo)")
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "Directorio de salida (default: el nombre inferido del título de la API)")
	return cmd
}
