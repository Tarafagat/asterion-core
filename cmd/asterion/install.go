package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"asterion-core/internal/prereqs"
	"asterion-core/internal/upgrade"
)

// installCmd es el punto de entrada para "traer" partes del ecosistema
// Asterion a este workspace — hoy solo tiene un subcomando
// (prerequirements), pero queda como grupo separado por si en el futuro
// hace falta instalar alguna otra cosa que no sea "los repos hermanos".
func installCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "install",
		Short: "Instala partes del ecosistema Asterion en este workspace",
	}
	root.AddCommand(installPrerequirementsCmd())
	return root
}

// installPrerequirementsCmd clona los repos hermanos que asterion-core
// necesita, la primera vez. Es el complemento de "asterion upgrade": ese
// solo actualiza lo que YA está clonado (ver internal/upgrade), esto trae
// lo que todavía falta.
func installPrerequirementsCmd() *cobra.Command {
	var dir string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "prerequirements",
		Short: "Clona los repos hermanos que asterion-core necesita (lab, language, plugin-contract, shared)",
		Long: "Clona, al lado de asterion-core, los repos hermanos que hacen falta para\n" +
			"compilar con todos sus módulos (asterion-lab, asterion-language,\n" +
			"asterion-plugin-contract — los tres con 'replace ../X' en go.mod) y para que\n" +
			"backend-core arme su entorno Python (asterion-shared). Un repo que ya está\n" +
			"clonado se deja tal cual — correr esto de nuevo es seguro.\n\n" +
			"No tiene nada que ver con 'asterion plugin install' (eso instala plugins de\n" +
			"TERCEROS en la carpeta de config de Asterion, no el propio código fuente del\n" +
			"ecosistema) ni con 'asterion cloud install-agent' (eso conecta una instancia\n" +
			"a un proyecto de Asterion Cloud). Una vez clonados, 'asterion upgrade' los\n" +
			"mantiene al día.",
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceDir := dir
			if workspaceDir == "" {
				var err error
				workspaceDir, err = upgrade.FindWorkspace("")
				if err != nil {
					return err
				}
			}
			results, err := prereqs.Install(workspaceDir)
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(map[string]any{"workspace": workspaceDir, "results": results})
				return nil
			}
			fmt.Printf("Workspace: %s\n\n", workspaceDir)
			printPrereqResults(results)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "Carpeta del workspace (default: buscar subiendo desde el directorio actual)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

func printPrereqResults(results []prereqs.Result) {
	anyCloned := false
	anyErr := false
	for _, r := range results {
		switch {
		case r.Error != "":
			anyErr = true
			fmt.Printf("✗ %s — %s\n", r.Name, r.Error)
		case r.Cloned:
			anyCloned = true
			fmt.Printf("✓ %s — clonado\n", r.Name)
		default:
			fmt.Printf("✓ %s — ya estaba\n", r.Name)
		}
	}
	if anyCloned {
		fmt.Println("\nSe clonaron repos nuevos — 'cd asterion-core && make install' (o 'go install ./cmd/asterion') para que el binario compile con todos sus módulos.")
	}
	if anyErr {
		fmt.Println("\nAlgún repo no se pudo clonar — ver el detalle arriba.")
	}
}
