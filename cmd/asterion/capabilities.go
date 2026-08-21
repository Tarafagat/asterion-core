package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

func providersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "Lista los proveedores registrados en asterion-core",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			providers, err := client.Providers()
			if err != nil {
				return err
			}
			for _, p := range providers {
				fmt.Println(p)
			}
			return nil
		},
	}
}

func capabilitiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities <provider>",
		Short: "Muestra qué capabilities declara un proveedor (ej. asterion capabilities aws)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			caps, err := client.Capabilities(args[0])
			if err != nil {
				return err
			}

			keys := make([]string, 0, len(caps))
			for k := range caps {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			fmt.Printf("%s\n%s\n\n", args[0], "────────────────────────")
			for _, k := range keys {
				mark := "✓"
				if !caps[k] {
					mark = "✗"
				}
				fmt.Printf("%-14s %s\n", k, mark)
			}
			return nil
		},
	}
}
