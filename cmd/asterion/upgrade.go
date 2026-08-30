package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"asterion-core/internal/upgrade"
)

// upgradeCmd es distinto de 'plugin update': ese actualiza plugins de
// TERCEROS instalados en ~/.config/asterion/plugins/ (ver plugin_update.go).
// Este actualiza el propio workspace de desarrollo del ecosistema Asterion
// (asterion-core, asterion-language, asterion-plugin-contract, etc.) — no
// tiene nada que ver con esa carpeta de config, y no la toca. Solo tiene
// sentido corrido dentro de (o apuntando a, con --dir) un checkout de
// desarrollo con varios repos 'asterion-*' hermanos.
func upgradeCmd() *cobra.Command {
	var dir string
	var list bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "upgrade [name]",
		Short: "git pull sobre los repos hermanos del ecosistema Asterion en tu workspace de desarrollo",
		Long: "Actualiza (git pull --ff-only) los repos 'asterion-*' que viven al lado de\n" +
			"asterion-core en tu checkout de desarrollo — asterion-language,\n" +
			"asterion-plugin-contract, asterion-lab, etc. Sin nombre, actualiza todos los que\n" +
			"encuentre; con un nombre, solo ese uno.\n\n" +
			"No tiene nada que ver con 'asterion plugin update': eso administra plugins de\n" +
			"terceros instalados en ~/.config/asterion/plugins/. Esto es exclusivamente el\n" +
			"workspace de desarrollo del propio ecosistema — asterion-plugin-contract es un\n" +
			"repo más de la lista, aunque asterion-core y asterion-language lo referencien cada\n" +
			"uno con su propio 'replace' en go.mod: en el filesystem es una sola carpeta, se\n" +
			"actualiza una sola vez.\n\n" +
			"Por default busca el workspace subiendo desde el directorio actual hasta encontrar\n" +
			"una carpeta con 'asterion-core' adentro — pasá --dir si corrés esto desde otro lado.\n" +
			"Nunca hace merge ni fuerza nada: un repo con cambios propios sin commitear, o cuya\n" +
			"rama divergió de la remota, se reporta tal cual lo rechaza git, sin tocarlo.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceDir := dir
			if workspaceDir == "" {
				var err error
				workspaceDir, err = upgrade.FindWorkspace("")
				if err != nil {
					return err
				}
			}

			if list {
				repos, err := upgrade.ListRepos(workspaceDir)
				if err != nil {
					return err
				}
				if asJSON {
					printJSON(map[string]any{"workspace": workspaceDir, "repos": repos})
					return nil
				}
				fmt.Printf("Workspace: %s\n\n", workspaceDir)
				for _, r := range repos {
					fmt.Println(" -", r)
				}
				return nil
			}

			name := ""
			if len(args) == 1 {
				name = args[0]
			}

			if name != "" {
				result, err := upgrade.Update(workspaceDir, name)
				if err != nil {
					return err
				}
				if asJSON {
					printJSON(result)
					if result.Error != "" {
						return fmt.Errorf("%s", result.Error)
					}
					return nil
				}
				printUpgradeResults([]upgrade.Result{result})
				if result.Error != "" {
					return fmt.Errorf("no se pudo actualizar %q — ver el detalle arriba", name)
				}
				return nil
			}

			results, err := upgrade.UpdateAll(workspaceDir)
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(map[string]any{"workspace": workspaceDir, "results": results})
				return nil
			}
			fmt.Printf("Workspace: %s\n\n", workspaceDir)
			printUpgradeResults(results)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "Carpeta del workspace (default: buscar subiendo desde el directorio actual)")
	cmd.Flags().BoolVar(&list, "list", false, "Solo listar los repos que encontraría, sin actualizar nada")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

func printUpgradeResults(results []upgrade.Result) {
	anyChanged := false
	anyErr := false
	for _, r := range results {
		switch {
		case r.Error != "":
			anyErr = true
			fmt.Printf("✗ %s — %s\n", r.Name, r.Error)
		case r.Changed:
			anyChanged = true
			fmt.Printf("✓ %s — actualizado\n", r.Name)
		default:
			fmt.Printf("✓ %s — ya estaba al día\n", r.Name)
		}
	}
	if anyChanged {
		fmt.Println("\nAlgún repo trajo código nuevo — puede hacer falta recompilar lo que dependa de él.")
	}
	if anyErr {
		fmt.Println("\nAlgún repo no se pudo actualizar — puede tener cambios propios sin commitear, o haber divergido de su rama remota (ver el detalle arriba).")
	}
}
