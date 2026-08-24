package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"asterion-lab"
)

// containerCmd administra contenedores Docker dentro de un
// laboratorio — análogo a vmCmd, pero para el backend Docker en vez de
// QEMU. Un contenedor "pertenece" a un laboratorio igual que una VM, se
// declara en la misma sección `containers:` del YAML.
func containerCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "container",
		Short: "Ejecutar comandos, ver logs y listar contenedores Docker dentro de un laboratorio",
	}
	root.AddCommand(containerListCmd(), containerExecCmd(), containerLogsCmd())
	return root
}

func containerListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista todos los contenedores de todos los laboratorios",
		RunE: func(cmd *cobra.Command, args []string) error {
			labs, err := lab.ListLabs()
			if err != nil {
				return err
			}
			type row struct {
				Lab       string             `json:"lab"`
				Container lab.ContainerState `json:"container"`
			}
			var rows []row
			for i := range labs {
				lab.RefreshLabStatus(&labs[i])
				for _, c := range labs[i].Containers {
					rows = append(rows, row{Lab: labs[i].Spec.Name, Container: c})
				}
			}
			printJSON(rows)
			return nil
		},
	}
}

func containerExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec <laboratorio> <contenedor> <comando...>",
		Short: "Ejecuta un comando dentro de un contenedor del laboratorio (docker exec) y muestra su salida",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, c, err := lab.FindContainer(args[0], args[1])
			if err != nil {
				return err
			}
			command := args[2]
			for _, extra := range args[3:] {
				command += " " + extra
			}
			out, exitCode, err := lab.ContainerExec(c, command)
			fmt.Print(out)
			if err != nil {
				return err
			}
			if exitCode != 0 {
				return fmt.Errorf("el comando salió con código %d", exitCode)
			}
			return nil
		},
	}
}

func containerLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs <laboratorio> <contenedor>",
		Short: "Muestra los logs de un contenedor del laboratorio",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, c, err := lab.FindContainer(args[0], args[1])
			if err != nil {
				return err
			}
			out, err := lab.ContainerLogs(c)
			fmt.Print(out)
			return err
		},
	}
}
