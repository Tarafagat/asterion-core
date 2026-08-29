package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"asterion-core/internal/localstore"
)

// instancesCmd es intencionalmente dual: sin --project opera sobre un
// inventario 100% local (no requiere sesión ni tocar la API), con
// --project opera sobre las instancias reales de un proyecto de Asterion
// Cloud (requiere 'asterion cloud login').
func instancesCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "instances",
		Short: "Instancias — locales (perfiles SSH propios) o de un proyecto en Asterion Cloud",
	}

	var projectSlug string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "Lista tus instancias locales, o las de un proyecto con --project",
		RunE: func(cmd *cobra.Command, args []string) error {
			if projectSlug != "" {
				client, err := newAPIClient()
				if err != nil {
					return err
				}
				instances, err := client.ListInstances(projectSlug)
				if err != nil {
					return err
				}
				printJSON(instances)
				return nil
			}

			instances, err := localstore.List()
			if err != nil {
				return err
			}
			if len(instances) == 0 {
				fmt.Println("No hay instancias locales todavía. Agregá una con 'asterion instances add'.")
				return nil
			}
			printJSON(instances)
			return nil
		},
	}
	listCmd.Flags().StringVar(&projectSlug, "project", "", "Proyecto de Asterion Cloud (si se omite, lista instancias locales)")

	var name, host, user, identityFile string
	var port int
	addCmd := &cobra.Command{
		Use:   "add",
		Short: "Agrega una instancia/host a tu inventario local (no requiere sesión)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := localstore.Add(localstore.Instance{
				Name: name, Host: host, Port: port, User: user, IdentityFile: identityFile,
			}); err != nil {
				return err
			}
			fmt.Printf("Instancia local %q agregada.\n", name)
			return nil
		},
	}
	addCmd.Flags().StringVar(&name, "name", "", "Nombre para identificarla (obligatorio)")
	addCmd.Flags().StringVar(&host, "host", "", "Host o IP (obligatorio)")
	addCmd.Flags().IntVar(&port, "port", 22, "Puerto SSH")
	addCmd.Flags().StringVar(&user, "user", "root", "Usuario SSH")
	addCmd.Flags().StringVar(&identityFile, "identity-file", "", "Ruta a la llave privada (opcional, usa el agente SSH si se omite)")
	_ = addCmd.MarkFlagRequired("name")
	_ = addCmd.MarkFlagRequired("host")

	removeCmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Quita una instancia de tu inventario local",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := localstore.Remove(args[0]); err != nil {
				return err
			}
			fmt.Printf("Instancia local %q eliminada.\n", args[0])
			return nil
		},
	}

	connectCmd := &cobra.Command{
		Use:   "connect <name>",
		Short: "Abre una sesión SSH a una instancia de tu inventario local",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instance, err := localstore.Get(args[0])
			if err != nil {
				return err
			}
			sshArgs := []string{"-p", fmt.Sprintf("%d", instance.Port)}
			if instance.IdentityFile != "" {
				sshArgs = append(sshArgs, "-i", instance.IdentityFile)
			}
			sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", instance.User, instance.Host))

			sshCmd := exec.Command("ssh", sshArgs...)
			sshCmd.Stdin, sshCmd.Stdout, sshCmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return sshCmd.Run()
		},
	}

	root.AddCommand(listCmd, addCmd, removeCmd, connectCmd)
	return root
}
