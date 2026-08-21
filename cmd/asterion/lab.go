package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"asterion-core/internal/lab"
)

// labCmd es el Asterion Infrastructure Safety Lab: crear máquinas
// desechables para probar cambios de infraestructura (firewall, SSH,
// reverse proxy, tunnel) sin arriesgar producción — spec "Infrastructure
// Safety Lab" §1-2.
//
// Estado real: ver internal/lab/lab.go. Sin un backend de virtualización
// disponible (Docker o QEMU+KVM), estos comandos fallan con un mensaje
// claro en vez de simular que crearon algo — nunca van a mostrar un
// "✓ VM creada" si no se creó una VM de verdad.
func labCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "lab",
		Short: "Infrastructure Safety Lab: entornos desechables para probar cambios de infraestructura antes de producción",
	}
	root.AddCommand(
		labCreateCmd(), labListCmd(), labStatusCmd(), labExecCmd(),
		labDestroyCmd(), labTestCmd(), labRunCmd(),
	)
	return root
}

func requireLabBackend() (lab.Backend, error) {
	return lab.DetectBackend()
}

func labCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <imagen>",
		Short: "Crea un entorno desechable (ej. ubuntu-nginx)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := requireLabBackend()
			if err != nil {
				return err
			}
			env, err := backend.Create(lab.Spec{Image: args[0]})
			if err != nil {
				return err
			}
			printJSON(env)
			return nil
		},
	}
}

func labListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista los entornos desechables activos",
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := requireLabBackend()
			if err != nil {
				return err
			}
			envs, err := backend.List()
			if err != nil {
				return err
			}
			printJSON(envs)
			return nil
		},
	}
}

func labStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Qué backend del laboratorio está disponible en esta máquina, si alguno",
		RunE: func(cmd *cobra.Command, args []string) error {
			results := map[string]any{}
			for _, b := range []lab.Backend{lab.DockerBackend{}, lab.QEMUBackend{}} {
				ok, detail := b.Available()
				entry := map[string]any{"available": ok}
				if !ok {
					entry["detail"] = detail
				}
				results[b.Name()] = entry
			}
			printJSON(results)
			return nil
		},
	}
}

func labExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec <id> <comando...>",
		Short: "Ejecuta un comando dentro de un entorno desechable",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := requireLabBackend()
			if err != nil {
				return err
			}
			out, err := backend.Exec(args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
}

func labDestroyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "destroy <id>",
		Short: "Destruye un entorno desechable y libera sus recursos",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := requireLabBackend()
			if err != nil {
				return err
			}
			return backend.Destroy(args[0])
		},
	}
}

func labTestCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "test [ssh|firewall|proxy|rollback]",
		Short: "Corre un escenario del laboratorio (o todos con --all) contra un entorno desechable real",
		Long: "Requiere un backend de laboratorio disponible (Docker o QEMU+KVM) — no hay una versión\n" +
			"simulada de estas pruebas: si no hay backend, el comando falla en vez de reportar un\n" +
			"resultado inventado.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !all && len(args) == 0 {
				return fmt.Errorf("especificá un escenario (ssh|firewall|proxy|rollback) o --all")
			}
			_, err := requireLabBackend()
			if err != nil {
				return fmt.Errorf("no se puede correr ningún test del laboratorio: %w", err)
			}
			// Con un backend real disponible, acá se instanciarían y
			// correrían los escenarios (spec §3-6) contra un Environment
			// recién creado. Ningún backend concreto implementa Create
			// todavía (ver internal/lab), así que este punto no se
			// alcanza hoy — se deja explícito en vez de simular un PASS.
			return fmt.Errorf("backend detectado pero los escenarios de prueba todavía no están implementados (ver internal/lab/lab.go)")
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Correr todos los escenarios")
	return cmd
}

func labRunCmd() *cobra.Command {
	var scenario string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Crea un entorno efímero, corre un escenario, y lo destruye — todo en un solo comando",
		RunE: func(cmd *cobra.Command, args []string) error {
			if scenario == "" {
				return fmt.Errorf("falta --scenario")
			}
			_, err := requireLabBackend()
			if err != nil {
				return fmt.Errorf("no se puede correr el escenario %q: %w", scenario, err)
			}
			return fmt.Errorf("backend detectado pero el flujo create→test→destroy todavía no está implementado (ver internal/lab/lab.go)")
		},
	}
	cmd.Flags().StringVar(&scenario, "scenario", "", "Nombre del escenario a correr (ej. firewall-ssh)")
	return cmd
}
