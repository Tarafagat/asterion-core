package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"asterion-lab"
)

// labCmd es Asterion Lab: laboratorios de infraestructura reproducibles
// definidos en YAML — crear, arrancar, parar, destruir. Cada laboratorio
// es un conjunto de VMs reales (QEMU) y/o contenedores reales (Docker)
// con red privada propia, pensado sobre todo para probar reglas de
// firewall o una imagen Docker propia antes de aplicarlas/publicarlas
// contra algo real. Se invoca directo desde el binario `asterion` (este
// repo, asterion-core) pero toda la lógica vive en el módulo Go
// asterion-lab, un repo hermano — ver su propio README para el detalle
// de arquitectura, y la sección "Asterion Lab" más abajo en este README
// para el ejemplo probado en vivo de punta a punta.
func labCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "lab",
		Short: "Laboratorios de infraestructura reproducibles (VMs QEMU y/o contenedores Docker definidos en YAML)",
	}
	root.AddCommand(
		labCreateCmd(), labStartCmd(), labStopCmd(), labDestroyCmd(),
		labListCmd(), labStatusCmd(), labTestCmd(), labRunCmd(),
	)
	return root
}

func resolveLab(nameOrID string) (lab.LabState, error) {
	if state, err := lab.LoadState(nameOrID); err == nil {
		return state, nil
	}
	return lab.FindLabByName(nameOrID)
}

func labCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <archivo.yaml>",
		Short: "Provisiona las VMs y contenedores de un laboratorio — no los arranca todavía",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := lab.LoadSpec(args[0])
			if err != nil {
				return err
			}
			state, err := lab.CreateLab(spec)
			if err != nil {
				return err
			}
			fmt.Printf(
				"✓ Laboratorio %q creado (id %s), %d VM(s) y %d contenedor(es) provisionados\n",
				state.Spec.Name, state.ID, len(state.VMs), len(state.Containers),
			)
			fmt.Printf("\nArrancalo con: asterion lab start %s\n", state.Spec.Name)
			return nil
		},
	}
}

func labStartCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "start <nombre>",
		Short: "Arranca todas las VMs del laboratorio, espera SSH, y aplica las reglas de firewall declaradas",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			if err := lab.StartLab(&state); err != nil {
				return err
			}
			if asJSON {
				printJSON(state)
				return nil
			}
			fmt.Printf("✓ Laboratorio %q corriendo\n", state.Spec.Name)
			for _, vm := range state.VMs {
				fmt.Printf("  - %s: ssh %s@127.0.0.1 -p %d (asterion vm ssh %s %s)\n", vm.Name, vm.SSHUser, vm.CtrlPort, state.Spec.Name, vm.Name)
			}
			for _, c := range state.Containers {
				fmt.Printf("  - %s: contenedor %s (asterion container exec %s %s \"...\")\n", c.Name, c.Image, state.Spec.Name, c.Name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el estado resultante como JSON")
	return cmd
}

func labStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <nombre>",
		Short: "Detiene todas las VMs del laboratorio sin borrar nada",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			if err := lab.StopLab(&state); err != nil {
				return err
			}
			fmt.Printf("✓ Laboratorio %q detenido\n", state.Spec.Name)
			return nil
		},
	}
}

func labDestroyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "destroy <nombre>",
		Short: "Detiene (si estaba corriendo) y borra el laboratorio entero: discos, seeds, logs, clave SSH",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			if err := lab.DestroyLab(state); err != nil {
				return err
			}
			fmt.Printf("✓ Laboratorio %q destruido\n", state.Spec.Name)
			return nil
		},
	}
}

func labListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista los laboratorios conocidos en esta máquina",
		RunE: func(cmd *cobra.Command, args []string) error {
			labs, err := lab.ListLabs()
			if err != nil {
				return err
			}
			for i := range labs {
				lab.RefreshLabStatus(&labs[i])
			}
			printJSON(labs)
			return nil
		},
	}
}

func labStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <nombre>",
		Short: "Estado real de un laboratorio (reconcilia cada VM contra si su proceso sigue vivo)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			lab.RefreshLabStatus(&state)
			_ = lab.SaveState(state)
			printJSON(state)
			return nil
		},
	}
}

func labTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <nombre>",
		Short: "Corre las aserciones de 'tests:' del YAML contra el laboratorio ya corriendo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			results, err := lab.RunTests(state)
			if err != nil {
				return err
			}
			printJSON(results)
			failed := 0
			for _, r := range results {
				if !r.Passed {
					failed++
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d/%d tests fallaron", failed, len(results))
			}
			return nil
		},
	}
}

func labRunCmd() *cobra.Command {
	var keepOnFailure bool
	cmd := &cobra.Command{
		Use:   "run <archivo.yaml>",
		Short: "Laboratorio efímero: crea, arranca, corre los tests, y destruye todo — un solo comando",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := lab.LoadSpec(args[0])
			if err != nil {
				return err
			}
			state, err := lab.CreateLab(spec)
			if err != nil {
				return err
			}
			fmt.Printf("✓ Laboratorio %q creado (id %s)\n", state.Spec.Name, state.ID)

			destroy := func() {
				if err := lab.DestroyLab(state); err != nil {
					fmt.Fprintln(os.Stderr, "aviso: no pude destruir el laboratorio:", err)
				} else {
					fmt.Printf("✓ Laboratorio %q destruido\n", state.Spec.Name)
				}
			}

			if err := lab.StartLab(&state); err != nil {
				if !keepOnFailure {
					destroy()
				}
				return err
			}
			fmt.Printf("✓ Laboratorio %q corriendo — corriendo tests\n", state.Spec.Name)

			results, testErr := lab.RunTests(state)
			printJSON(results)

			if !keepOnFailure {
				destroy()
			} else if testErr != nil || anyFailed(results) {
				fmt.Printf("Laboratorio %q dejado corriendo (--keep-on-failure) para inspeccionar: asterion lab destroy %s cuando termines\n", state.Spec.Name, state.Spec.Name)
			} else {
				destroy()
			}

			if testErr != nil {
				return testErr
			}
			if anyFailed(results) {
				return fmt.Errorf("algunos tests fallaron — ver detalle arriba")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepOnFailure, "keep-on-failure", false, "No destruir el laboratorio si algún test falla, para poder inspeccionarlo")
	return cmd
}

func anyFailed(results []lab.TestResult) bool {
	for _, r := range results {
		if !r.Passed {
			return true
		}
	}
	return false
}
