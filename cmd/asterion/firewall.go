package main

import (
	"github.com/spf13/cobra"

	"asterion-core/internal/safety"
)

// firewallCmd agrupa operaciones de firewall — hoy solo `plan`, de solo
// lectura (spec §12). No hay `firewall apply`: el UFWAdapter todavía no
// declara la capability CapApply (ver internal/safety/adapters.go), así
// que no existe ninguna operación que la requiera.
func firewallCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "firewall",
		Short: "Planificación de firewall (solo lectura — no hay 'apply' todavía, ver 'asterion local doctor')",
	}
	root.AddCommand(firewallPlanCmd())
	return root
}

func firewallPlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Analiza el estado actual de SSH y firewall y arma una propuesta — nunca modifica nada",
		Long: "100% de solo lectura: descubre el puerto real de SSH, si el firewall protege ese puerto,\n" +
			"y qué riesgo tendría endurecer la política por defecto. No existe un modo --apply — Asterion\n" +
			"no aplica cambios de firewall todavía (ver asterion-core/README.md § Safety Lab).",
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := safety.BuildFirewallPlan()
			if err != nil {
				return err
			}
			printJSON(plan)
			return nil
		},
	}
}
