package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"asterion-lab"
)

// vmCmd administra VMs puntuales dentro de un laboratorio — conectarse,
// ejecutar comandos, clonar, listar. La creación/arranque/parada de VMs
// "de a una" vive bajo `asterion lab` porque en este diseño toda VM
// pertenece a un laboratorio (aunque sea de una sola VM) — un solo modelo,
// sin dos caminos de código distintos para lo mismo.
func vmCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "vm",
		Short: "Conectarse, ejecutar comandos, clonar y listar VMs dentro de un laboratorio",
	}
	root.AddCommand(vmListCmd(), vmSSHCmd(), vmExecCmd(), vmCloneCmd(), vmSnapshotCmd())
	return root
}

func findVM(state lab.LabState, name string) (lab.VMState, error) {
	for _, vm := range state.VMs {
		if vm.Name == name {
			return vm, nil
		}
	}
	return lab.VMState{}, fmt.Errorf("no hay ninguna VM %q en el laboratorio %q", name, state.Spec.Name)
}

func vmListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista todas las VMs de todos los laboratorios",
		RunE: func(cmd *cobra.Command, args []string) error {
			labs, err := lab.ListLabs()
			if err != nil {
				return err
			}
			type row struct {
				Lab string      `json:"lab"`
				VM  lab.VMState `json:"vm"`
			}
			var rows []row
			for i := range labs {
				lab.RefreshLabStatus(&labs[i])
				for _, vm := range labs[i].VMs {
					rows = append(rows, row{Lab: labs[i].Spec.Name, VM: vm})
				}
			}
			printJSON(rows)
			return nil
		},
	}
}

func vmSSHCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh <laboratorio> <vm>",
		Short: "Abre una sesión SSH interactiva contra una VM del laboratorio",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			vm, err := findVM(state, args[1])
			if err != nil {
				return err
			}
			sshArgs := []string{
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "LogLevel=ERROR",
				"-i", state.SSHKeyPath,
				"-p", fmt.Sprintf("%d", vm.CtrlPort),
				vm.SSHUser + "@127.0.0.1",
			}
			sshCmd := exec.Command("ssh", sshArgs...)
			sshCmd.Stdin, sshCmd.Stdout, sshCmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return sshCmd.Run()
		},
	}
}

func vmExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec <laboratorio> <vm> <comando...>",
		Short: "Ejecuta un comando dentro de una VM del laboratorio y muestra su salida",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			vm, err := findVM(state, args[1])
			if err != nil {
				return err
			}
			command := args[2]
			for _, extra := range args[3:] {
				command += " " + extra
			}
			out, exitCode, err := lab.Exec(vm, state.SSHKeyPath, command)
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

func vmCloneCmd() *cobra.Command {
	var cpus, memoryMB, diskGB int
	cmd := &cobra.Command{
		Use:   "clone <laboratorio> <vm> <nombre-nuevo>",
		Short: "Crea una VM independiente a partir del disco actual de otra (backing file de qcow2, instantáneo)",
		Long: "Funciona con la VM origen corriendo o detenida. Si está corriendo, la clona en " +
			"caliente sin apagarla: congela su disco actual vía QMP (blockdev-snapshot-sync) y la VM " +
			"sigue escribiendo a un overlay nuevo desde ese instante — el laboratorio origen queda " +
			"con su estado actualizado automáticamente.",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			newState, err := lab.CloneVM(&state, args[1], args[2], "", cpus, memoryMB, diskGB)
			if err != nil {
				return err
			}
			fmt.Printf("✓ %q clonada como %q (laboratorio %s)\n", args[1], args[2], newState.ID)
			fmt.Printf("\nArrancala con: asterion lab start %s\n", newState.Spec.Name)
			return nil
		},
	}
	cmd.Flags().IntVar(&cpus, "cpu", 1, "CPUs de la VM clonada")
	cmd.Flags().IntVar(&memoryMB, "memory-mb", 1024, "Memoria (MB) de la VM clonada")
	cmd.Flags().IntVar(&diskGB, "disk-gb", 0, "Redimensionar el disco clonado a este tamaño (0 = dejarlo como está)")
	return cmd
}

func vmSnapshotCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "snapshot",
		Short: "Snapshots internos del disco de una VM (qcow2) — funciona con la VM corriendo o detenida",
	}
	root.AddCommand(vmSnapshotCreateCmd(), vmSnapshotRestoreCmd(), vmSnapshotListCmd(), vmSnapshotDeleteCmd())
	return root
}

func vmSnapshotCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <laboratorio> <vm> <nombre>",
		Short: "Crea un snapshot del estado actual del disco",
		Long: "Con la VM detenida usa qemu-img directamente sobre el archivo. Con la VM corriendo, " +
			"lo hace en caliente vía el monitor QMP (savevm): captura CPU, RAM y disco sin apagarla.",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			vm, err := findVM(state, args[1])
			if err != nil {
				return err
			}
			if err := lab.SnapshotVM(vm, args[2], false); err != nil {
				return err
			}
			fmt.Printf("✓ Snapshot %q creado sobre %q\n", args[2], vm.Name)
			return nil
		},
	}
}

func vmSnapshotRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <laboratorio> <vm> <nombre>",
		Short: "Restaura el disco al estado de un snapshot previo",
		Long: "Con la VM detenida usa qemu-img directamente. Con la VM corriendo, restaura en " +
			"caliente vía QMP (loadvm) — la VM sigue corriendo, con su CPU/RAM/disco reemplazados " +
			"por los del snapshot.",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			vm, err := findVM(state, args[1])
			if err != nil {
				return err
			}
			if err := lab.SnapshotVM(vm, args[2], true); err != nil {
				return err
			}
			fmt.Printf("✓ %q restaurada al snapshot %q\n", vm.Name, args[2])
			return nil
		},
	}
}

func vmSnapshotListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <laboratorio> <vm>",
		Short: "Lista los snapshots del disco de una VM",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			vm, err := findVM(state, args[1])
			if err != nil {
				return err
			}
			out, err := lab.ListSnapshots(vm)
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		},
	}
}

func vmSnapshotDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <laboratorio> <vm> <nombre>",
		Short: "Borra un snapshot del disco de una VM",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := resolveLab(args[0])
			if err != nil {
				return err
			}
			vm, err := findVM(state, args[1])
			if err != nil {
				return err
			}
			if err := lab.DeleteSnapshot(vm, args[2]); err != nil {
				return err
			}
			fmt.Printf("✓ snapshot %q de %q borrado\n", args[2], vm.Name)
			return nil
		},
	}
}
