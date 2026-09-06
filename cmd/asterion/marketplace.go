package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"asterion-core/internal/plugins"
)

// marketplaceCmd es la puerta de entrada, desde el CLI, al marketplace
// público de Asterion Cloud (los mismos plugins que se ven en
// asterioncloud.com/marketplace o en la pestaña "Mis plugins" del
// dashboard) — buscar, comprar (si es de pago) e instalar sin salir de la
// terminal. No reemplaza 'asterion plugin install <repo-url>': ese sigue
// siendo el camino directo a un repo de git propio o ajeno, adentro o
// afuera del marketplace, exactamente como antes.
func marketplaceCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "marketplace",
		Short: "Buscar, comprar e instalar plugins del marketplace de Asterion Cloud",
	}
	root.AddCommand(marketplaceSearchCmd(), marketplaceBuyCmd(), marketplaceInstallCmd())
	return root
}

func formatPluginPrice(plugin map[string]any) string {
	if isOfficial, _ := plugin["is_official"].(bool); isOfficial {
		return "oficial · gratis"
	}
	cents, ok := plugin["price_usd_cents"].(float64)
	if !ok || cents == 0 {
		return "gratis"
	}
	return fmt.Sprintf("$%.2f/mes", cents/100)
}

func marketplaceSearchCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Busca plugins en el marketplace público de Asterion Cloud",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := ""
			if len(args) == 1 {
				q = args[0]
			}
			client, err := newAPIClient()
			if err != nil {
				return err
			}
			results, err := client.SearchMarketplacePlugins(q)
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(results)
				return nil
			}
			if len(results) == 0 {
				fmt.Println("Ningún plugin coincide con la búsqueda.")
				return nil
			}
			for _, p := range results {
				slug, _ := p["slug"].(string)
				name, _ := p["name"].(string)
				desc, _ := p["description"].(string)
				author, _ := p["author_name"].(string)
				fmt.Printf("%s — %s (%s)\n", slug, name, formatPluginPrice(p))
				if desc != "" {
					fmt.Printf("  %s\n", desc)
				}
				fmt.Printf("  por %s\n", author)
			}
			fmt.Println("\nInstalá uno con: asterion marketplace install <slug>")
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir los resultados como JSON en vez de texto")
	return cmd
}

func marketplaceBuyCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "buy <slug>",
		Short: "Genera el checkout de MercadoPago para comprar un plugin de pago del marketplace",
		Long: "No hay forma de completar un pago real sin salir de la terminal — este comando\n" +
			"genera la preferencia de Checkout Pro y te da la URL para completarlo en el\n" +
			"navegador. Una vez aprobado el pago (Asterion Cloud lo confirma solo via su\n" +
			"webhook de MercadoPago), 'asterion marketplace install <slug>' ya funciona.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient()
			if err != nil {
				return err
			}
			result, err := client.CheckoutMarketplacePlugin(args[0])
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(result)
				return nil
			}
			initPoint, _ := result["init_point"].(string)
			fmt.Println("Completá el pago en:")
			fmt.Println("  " + initPoint)
			fmt.Printf("\nUna vez aprobado: asterion marketplace install %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

func marketplaceInstallCmd() *cobra.Command {
	var name string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "install <slug>",
		Short: "Instala un plugin del marketplace por su slug (resuelve su repo real y delega en 'plugin install')",
		Long: "Si el plugin es de pago y todavía no lo compraste, corta acá con un mensaje\n" +
			"claro en vez de fallar más abajo con un error de git confuso — comprálo primero\n" +
			"con 'asterion marketplace buy <slug>'.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient()
			if err != nil {
				return err
			}
			installed, err := plugins.InstallFromMarketplace(client, args[0], name)
			if err != nil {
				return err
			}
			contractErr := plugins.EnsureContractRepo()

			if asJSON {
				printJSON(installed)
				return nil
			}
			if contractErr != nil {
				fmt.Printf("⚠ No pude preparar asterion-plugin-contract de una (%s) — 'asterion plugin build %s' lo va a reintentar.\n", contractErr, installed.Name)
			}
			fmt.Printf("✓ Plugin %q instalado desde el marketplace (%s)\n", installed.Name, installed.Manifest.Version)
			fmt.Printf("\nArrancalo con: asterion plugin start %s\n", installed.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Nombre a usar si no coincide con el que declara el plugin.yaml")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el registro instalado como JSON en vez de texto")
	return cmd
}
