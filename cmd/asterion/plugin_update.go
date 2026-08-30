package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"asterion-core/internal/plugins"
)

// pluginUpdateCmd cierra el gap que el propio spec de APC dejó anotado a
// propósito ("no hay un paso de 'update' separado en v1... hasta que
// exista una necesidad real"): 'git pull --ff-only' sobre el repo clonado
// de un plugin instalado (o, con --all, de todos). Nunca toca plugins
// --link (ver plugins.Update) ni fuerza un merge — un repo que divergió o
// tiene cambios propios sin commitear se reporta tal cual lo rechaza git,
// no se pisa nada.
func pluginUpdateCmd() *cobra.Command {
	var all bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "update [name]",
		Short: "git pull sobre el repo clonado de un plugin instalado (o, con --all, todos)",
		Long: "Actualiza el código ya clonado en ~/.config/asterion/plugins/repos/<name> a lo último\n" +
			"de su rama configurada ('git pull --ff-only' — nunca crea un merge commit ni fuerza\n" +
			"nada: si el repo divergió o tiene cambios propios sin commitear, git lo rechaza solo y\n" +
			"eso se reporta tal cual, sin tocar nada. Con --all, un repo que falla no corta el resto.\n\n" +
			"Los plugins instalados con --link se saltan siempre (ver 'plugin install --help') — no\n" +
			"son un clone que Asterion administre, y ni siquiera necesitan ser un repo git.\n\n" +
			"Actualizar el código no reinicia el plugin ni lo recompila solo: para un plugin en Go,\n" +
			"'asterion plugin build <name>' después de esto, y parar/arrancar de nuevo el proceso\n" +
			"para que tome el cambio.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" && !all {
				return fmt.Errorf("pasá un nombre de plugin, o --all para actualizar todos los instalados (ver 'asterion plugin list')")
			}
			if name != "" && all {
				return fmt.Errorf("pasá un nombre o --all, no los dos")
			}

			if all {
				results, err := plugins.UpdateAll()
				if err != nil {
					return err
				}
				if asJSON {
					printJSON(results)
					return nil
				}
				printUpdateResults(results)
				return nil
			}

			result := plugins.Update(name)
			if asJSON {
				printJSON(result)
				if result.Error != "" {
					return fmt.Errorf("%s", result.Error)
				}
				return nil
			}
			printUpdateResults([]plugins.UpdateResult{result})
			if result.Error != "" {
				return fmt.Errorf("no se pudo actualizar %q — ver el detalle arriba", name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Actualizar todos los plugins instalados (salteando los --link)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

func printUpdateResults(results []plugins.UpdateResult) {
	anyChanged := false
	anyErr := false
	for _, r := range results {
		switch {
		case r.Error != "":
			anyErr = true
			fmt.Printf("✗ %s — %s\n", r.Name, r.Error)
		case r.Skipped:
			fmt.Printf("- %s — omitido (%s)\n", r.Name, r.Reason)
		case r.Changed:
			anyChanged = true
			fmt.Printf("✓ %s — actualizado\n", r.Name)
		default:
			fmt.Printf("✓ %s — ya estaba al día\n", r.Name)
		}
	}
	if anyChanged {
		fmt.Println("\nAlgún repo trajo código nuevo — recordá recompilar si aplica ('asterion plugin build <name>') y reiniciar el plugin para que tome el cambio.")
	}
	if anyErr {
		fmt.Println("\nAlgún repo no se pudo actualizar — puede tener cambios propios sin commitear, o haber divergido de su rama remota (ver el detalle arriba).")
	}
}
