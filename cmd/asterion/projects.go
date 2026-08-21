package main

import "github.com/spf13/cobra"

func projectsCmd() *cobra.Command {
	root := &cobra.Command{Use: "projects", Short: "Proyectos de Asterion"}

	root.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Lista tus proyectos",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient()
			if err != nil {
				return err
			}
			projects, err := client.ListProjects()
			if err != nil {
				return err
			}
			printJSON(projects)
			return nil
		},
	})

	return root
}
