package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// servicesCmd agrupa lo que un desarrollador ve del lado de Cloud del
// Service Registry — el lado de escritura ('asterion plugin set-service')
// vive en plugins.go, ya que es una propiedad de una instalación
// concreta, no de "servicios" como concepto de Cloud.
func servicesCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "services",
		Short: "Servicios registrados en un proyecto de Asterion Cloud (réplicas de plugins agrupadas por nombre)",
	}
	root.AddCommand(servicesListCmd())
	return root
}

func servicesListCmd() *cobra.Command {
	var projectSlug string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Lista los servicios de un proyecto y cuántas réplicas sanas tiene cada uno",
		Long: "Un servicio existe acá apenas alguna instancia reporta, por heartbeat, un plugin\n" +
			"instalado con 'asterion plugin set-service <nombre> <servicio>' — sin scheduler ni\n" +
			"ruteo automático todavía: esto es solo visibilidad de qué está corriendo dónde.",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient()
			if err != nil {
				return err
			}
			resolvedProjectSlug, err := resolveProjectSlug(client, projectSlug)
			if err != nil {
				return err
			}

			services, err := client.ListServices(resolvedProjectSlug)
			if err != nil {
				return err
			}

			if asJSON {
				printJSON(services)
				return nil
			}

			if len(services) == 0 {
				fmt.Println("Este proyecto todavía no tiene ningún servicio registrado — 'asterion plugin set-service <nombre> <servicio>' en una instancia con el plugin corriendo.")
				return nil
			}
			for _, svc := range services {
				name, _ := svc["name"].(string)
				protocol, _ := svc["protocol"].(string)
				replicas, _ := svc["replicas"].([]any)
				fmt.Printf("%s (%s) — %d réplica(s)\n", name, protocol, len(replicas))
				for _, r := range replicas {
					replica, ok := r.(map[string]any)
					if !ok {
						continue
					}
					instanceName, _ := replica["instance_name"].(string)
					health, _ := replica["health"].(string)
					port, _ := replica["port"].(float64)
					fmt.Printf("  ● %s — puerto %d — %s\n", instanceName, int(port), health)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&projectSlug, "project", "", "Proyecto de Asterion Cloud (opcional — si se omite, se elige interactivamente)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Salida en JSON, para scripts")
	return cmd
}
