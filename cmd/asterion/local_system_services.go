package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"asterion-core/internal/sysservices"
)

// localSystemServicesCmd vive bajo `local` (no como comando de nivel
// superior) por el mismo criterio que `local user`/`local status`: consulta
// ESTA máquina, sin hablar con Asterion Cloud. Ponerlo como
// `asterion system-services` de nivel superior además chocaría en la
// cabeza con el comando ya existente y no relacionado `asterion services`
// (el Service Registry de plugins, ver services.go).
func localSystemServicesCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "system-services",
		Short: "Unidades .service de systemd de ESTA máquina (todas, no solo las de Asterion)",
		Long: "Solo lectura, a propósito: no hay 'restart'/'stop'/'start' acá — si ya tenés acceso\n" +
			"a esta terminal, corré 'systemctl restart <unidad>' directo, es exactamente lo mismo\n" +
			"sin una capa intermedia. El control REMOTO (desde el dashboard de Asterion Cloud,\n" +
			"gateado por permisos y auditado) vive en la pestaña 'Servicios del sistema' de cada\n" +
			"instancia — ver 'asterion agent enable-service-control' para habilitarlo acá.",
	}
	root.AddCommand(localSystemServicesListCmd())
	return root
}

func localSystemServicesListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Lista todas las unidades .service (systemctl list-units --all --type=service)",
		RunE: func(cmd *cobra.Command, args []string) error {
			units, err := sysservices.List()
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(units)
				return nil
			}
			for _, u := range units {
				mark := ""
				if u.Protected {
					mark = "  [protegida — Asterion Cloud nunca la controla remotamente]"
				}
				fmt.Printf("%-45s %-10s %-10s %s%s\n", u.Name, u.ActiveState, u.SubState, u.Description, mark)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Salida en JSON, para scripts")
	return cmd
}
