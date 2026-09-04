package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"asterion-core/internal/localserve"
	"asterion-core/internal/runtime"
	"asterion-core/internal/tunnel"
)

// localRouteCmd resume, en un solo lugar, la "ruta" de esta máquina: el
// puerto donde escucha 'local serve --background' (si está corriendo), la
// URL pública del túnel (si hay uno activo y es modo "quick"), y si esa
// información se está reportando a Asterion Cloud vía el heartbeat del
// agente (report_local_serve, ver internal/runtime/config.go). Es un
// resumen legible pensado para uso interactivo — 'local status' ya
// devuelve todo esto (y más) como JSON crudo para scripting/doctor.
func localRouteCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "route",
		Short: "Muestra la ruta local (puerto de 'local serve', túnel activo) y si se está reportando a Asterion Cloud",
		RunE: func(cmd *cobra.Command, args []string) error {
			serveState, serving, err := localserve.Status()
			if err != nil {
				return err
			}
			tunnelState, tunneling, err := tunnel.Status()
			if err != nil {
				return err
			}
			cfg, err := runtime.LoadConfig()
			if err != nil {
				return err
			}

			if asJSON {
				var localServeJSON, tunnelJSON any
				if serving {
					localServeJSON = serveState
				}
				if tunneling {
					tunnelJSON = tunnelState
				}
				printJSON(map[string]any{
					"local_serve":        localServeJSON,
					"tunnel":             tunnelJSON,
					"report_local_serve": cfg.ReportLocalServe,
				})
				return nil
			}

			if serving {
				fmt.Printf("Local serve: corriendo en el puerto %d (http://127.0.0.1:%d)\n", serveState.Port, serveState.Port)
			} else {
				fmt.Println("Local serve: no está corriendo — 'asterion local serve --background' para levantarlo")
			}

			switch {
			case tunneling && tunnelState.URL != "":
				fmt.Printf("Túnel: corriendo (modo %s) → %s\n", tunnelState.Mode, tunnelState.URL)
			case tunneling:
				fmt.Printf("Túnel: corriendo (modo %s) — sin URL local que mostrar, el hostname vive en tu dashboard de Cloudflare\n", tunnelState.Mode)
			default:
				fmt.Println("Túnel: no está corriendo — 'asterion local tunnel start' para exponerlo")
			}

			if cfg.ReportLocalServe {
				fmt.Println("Reportar a Asterion Cloud: SÍ — esta ruta viaja en el próximo heartbeat del agente")
			} else {
				fmt.Println("Reportar a Asterion Cloud: NO — activalo con 'asterion local config set report_local_serve true'")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}
