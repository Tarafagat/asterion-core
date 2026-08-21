package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func cloudAccountsCmd() *cobra.Command {
	root := &cobra.Command{Use: "cloud-accounts", Short: "Cuentas cloud conectadas a un proyecto (AWS/Azure/GCP/OCI)"}

	var projectID int
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "Lista las cuentas cloud de un proyecto",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient()
			if err != nil {
				return err
			}
			accounts, err := client.ListCloudAccounts(projectID)
			if err != nil {
				return err
			}
			printJSON(accounts)
			return nil
		},
	}
	listCmd.Flags().IntVar(&projectID, "project", 0, "ID del proyecto")
	_ = listCmd.MarkFlagRequired("project")

	var providerID int
	var alias, externalAccountID, region string
	var creds []string
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Conecta una cuenta cloud nueva a un proyecto",
		RunE: func(cmd *cobra.Command, args []string) error {
			credentials := map[string]any{}
			for _, kv := range creds {
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("--cred debe tener el formato clave=valor (recibido %q)", kv)
				}
				credentials[parts[0]] = parts[1]
			}
			client, err := newAPIClient()
			if err != nil {
				return err
			}
			account, err := client.CreateCloudAccount(projectID, map[string]any{
				"provider_id":         providerID,
				"alias":               alias,
				"external_account_id": externalAccountID,
				"region_default":      region,
				"credentials":         credentials,
			})
			if err != nil {
				return err
			}
			printJSON(account)
			return nil
		},
	}
	createCmd.Flags().IntVar(&projectID, "project", 0, "ID del proyecto")
	createCmd.Flags().IntVar(&providerID, "provider", 0, "ID del proveedor (ver 'asterion providers')")
	createCmd.Flags().StringVar(&alias, "alias", "", "Alias de la cuenta (ej. prod-aws)")
	createCmd.Flags().StringVar(&externalAccountID, "external-id", "", "ID de cuenta/suscripción/proyecto/tenancy en el proveedor")
	createCmd.Flags().StringVar(&region, "region", "us-east-1", "Región por defecto")
	createCmd.Flags().StringArrayVar(&creds, "cred", nil, "Credencial clave=valor (repetible, ej. --cred access_key_id=AKIA...)")
	_ = createCmd.MarkFlagRequired("project")
	_ = createCmd.MarkFlagRequired("provider")
	_ = createCmd.MarkFlagRequired("alias")

	root.AddCommand(listCmd, createCmd)
	return root
}
