package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"asterion-core/internal/apiclient"
)

func provisionCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "provision",
		Short: "Motor de aprovisionamiento: describir, planificar, confirmar y aplicar",
		Long: "Ciclo completo: describe -> plan -> confirm -> apply.\n" +
			"Cada paso es explícito — 'apply' nunca se ejecuta sin haber pasado por 'plan' y 'confirm' antes.",
	}
	root.AddCommand(provisionListCmd(), provisionDescribeCmd(), provisionStatusCmd(), provisionPlanCmd(), provisionConfirmCmd(), provisionApplyCmd())
	return root
}

func provisionListCmd() *cobra.Command {
	var projectSlug string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Lista las solicitudes de aprovisionamiento de un proyecto",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient()
			if err != nil {
				return err
			}
			reqs, err := client.ListProvisioningRequests(projectSlug)
			if err != nil {
				return err
			}
			printJSON(reqs)
			return nil
		},
	}
	cmd.Flags().StringVar(&projectSlug, "project", "", "Proyecto de Asterion Cloud")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

func provisionDescribeCmd() *cobra.Command {
	var projectSlug string
	var resourceType, specInline, specFile string
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Paso 1: describe qué querés crear (network | instance | managed_database | storage_bucket)",
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw []byte
			var err error
			switch {
			case specFile != "":
				raw, err = os.ReadFile(specFile)
			case specInline != "":
				raw = []byte(specInline)
			default:
				return fmt.Errorf("pasá --spec '{...}' o --spec-file archivo.json con la descripción del recurso")
			}
			if err != nil {
				return err
			}
			var spec map[string]any
			if err := json.Unmarshal(raw, &spec); err != nil {
				return fmt.Errorf("el spec no es JSON válido: %w", err)
			}

			client, err := newAPIClient()
			if err != nil {
				return err
			}
			req, err := client.CreateProvisioningRequest(projectSlug, resourceType, spec)
			if err != nil {
				return err
			}
			printJSON(req)
			return nil
		},
	}
	cmd.Flags().StringVar(&projectSlug, "project", "", "Proyecto de Asterion Cloud")
	cmd.Flags().StringVar(&resourceType, "type", "", "Tipo de recurso: network | instance | managed_database | storage_bucket")
	cmd.Flags().StringVar(&specInline, "spec", "", "Descripción del recurso como JSON inline")
	cmd.Flags().StringVar(&specFile, "spec-file", "", "Descripción del recurso como archivo JSON")
	_ = cmd.MarkFlagRequired("project")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

func provisionStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <request-id>",
		Short: "Muestra el estado y el plan de una solicitud",
		Args:  cobra.ExactArgs(1),
		RunE:  provisioningAction(func(c *apiclient.Client, id int) (any, error) { return c.GetProvisioningRequest(id) }),
	}
}

func provisionPlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan <request-id>",
		Short: "Pasos 2-4: valida, arma el plan (DAG de pasos) y estima el costo mensual",
		Args:  cobra.ExactArgs(1),
		RunE:  provisioningAction(func(c *apiclient.Client, id int) (any, error) { return c.PlanProvisioningRequest(id) }),
	}
}

func provisionConfirmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "confirm <request-id>",
		Short: "Paso 5: confirma un plan ya estimado",
		Args:  cobra.ExactArgs(1),
		RunE:  provisioningAction(func(c *apiclient.Client, id int) (any, error) { return c.ConfirmProvisioningRequest(id) }),
	}
}

func provisionApplyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply <request-id>",
		Short: "Pasos 6-7: aplica cada paso del plan en orden y verifica el resultado",
		Args:  cobra.ExactArgs(1),
		RunE:  provisioningAction(func(c *apiclient.Client, id int) (any, error) { return c.ApplyProvisioningRequest(id) }),
	}
}

func provisioningAction(fn func(c *apiclient.Client, id int) (any, error)) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("el id de la solicitud debe ser numérico: %w", err)
		}
		client, err := newAPIClient()
		if err != nil {
			return err
		}
		result, err := fn(client, id)
		if err != nil {
			return err
		}
		printJSON(result)
		return nil
	}
}
