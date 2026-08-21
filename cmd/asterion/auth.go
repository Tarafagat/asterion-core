package main

import (
	"github.com/spf13/cobra"

	"asterion-core/internal/cliconfig"
)

func whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Muestra el usuario autenticado actualmente",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient()
			if err != nil {
				return err
			}
			me, err := client.Me()
			if err != nil {
				return err
			}
			printJSON(me)
			return nil
		},
	}
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Muestra la configuración local del CLI",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return err
			}
			printJSON(cfg)
			return nil
		},
	}
	return cmd
}
