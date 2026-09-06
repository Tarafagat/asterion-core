package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"asterion-core/internal/osuser"
	"asterion-core/internal/safety"
)

// localUserCmd crea/administra usuarios del SISTEMA OPERATIVO en ESTA
// máquina — el mismo motor (internal/osuser) que usa el camino remoto por
// SSH de Asterion Cloud para instancias que no tienen a `asterion` mismo
// corriendo adentro. Localizar la instancia y autenticarse ya están
// resueltos por el simple hecho de que este comando corre en la propia
// máquina destino — lo que sigue (crear el usuario, su clave SSH, sus
// grupos, su sudo) es idéntico sin importar el proveedor cloud de abajo.
func localUserCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "user",
		Short: "Usuarios del sistema operativo en esta máquina (crear, listar, quitar) — con niveles de acceso fijos",
	}
	root.AddCommand(localUserCreateCmd(), localUserListCmd(), localUserRemoveCmd())
	return root
}

func localUserCreateCmd() *cobra.Command {
	var level string
	var groupsCSV string
	var sshKey string
	var generateKey bool
	var yes bool
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "create <username>",
		Short: "Crea (o ajusta) un usuario del sistema con un nivel de acceso — muestra el plan y pide confirmar antes de aplicar",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]

			if sshKey != "" && generateKey {
				return fmt.Errorf("usar --ssh-key o --generate-key, no los dos")
			}

			var privateKeyToShow string
			if generateKey {
				priv, pub, err := osuser.GenerateKeyPair("asterion-" + username)
				if err != nil {
					return err
				}
				privateKeyToShow = priv
				sshKey = pub
			}

			var extraGroups []string
			if groupsCSV != "" {
				for _, g := range strings.Split(groupsCSV, ",") {
					if g = strings.TrimSpace(g); g != "" {
						extraGroups = append(extraGroups, g)
					}
				}
			}

			spec := osuser.Spec{
				Username:    username,
				Level:       osuser.Level(level),
				ExtraGroups: extraGroups,
				PublicKey:   sshKey,
			}

			diff, err := osuser.Plan(spec)
			if err != nil {
				return err
			}

			// --json es para invocación no interactiva (backend-core, el
			// dashboard de 'local serve') — nunca espera un prompt en
			// stdin, así que implica --yes sin necesidad de pasarla aparte.
			if !asJSON {
				printUserPlan(diff)
			}
			if !yes && !asJSON {
				fmt.Print("\n¿Aplicar estos cambios? [s/N]: ")
				answer := strings.ToLower(strings.TrimSpace(trimNewline(readLine())))
				if answer != "s" && answer != "si" && answer != "y" && answer != "yes" {
					fmt.Println("Cancelado — no se modificó nada.")
					return nil
				}
			}

			if err := safety.RequireSafeApply(safety.OSUserAdapter{}); err != nil {
				return err
			}

			result, err := osuser.Apply(diff)
			if err != nil {
				return err
			}
			if err := osuser.RecordApply(result.Diff); err != nil {
				return fmt.Errorf("se aplicó, pero no se pudo registrar localmente: %w", err)
			}

			if asJSON {
				printJSON(map[string]any{"result": result, "private_key": privateKeyToShow})
				return nil
			}

			fmt.Printf("\n✓ Usuario %q listo (nivel %s)\n", username, level)
			for _, w := range result.Warnings {
				fmt.Printf("  ⚠ %s\n", w)
			}
			if privateKeyToShow != "" {
				fmt.Println("\nClave privada generada — copiala ahora, no se vuelve a mostrar:")
				fmt.Println(privateKeyToShow)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&level, "level", "", "Nivel de acceso: admin, operador o solo_lectura")
	cmd.Flags().StringVar(&groupsCSV, "group", "", "Grupos adicionales, separados por coma")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "Línea de clave pública a instalar (ej. \"ssh-ed25519 AAAA... yo\")")
	cmd.Flags().BoolVar(&generateKey, "generate-key", false, "Generar un par de claves Ed25519 nuevo para este usuario")
	cmd.Flags().BoolVar(&yes, "yes", false, "No pedir confirmación antes de aplicar")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto (lo usa backend-core) — implica --yes")
	_ = cmd.MarkFlagRequired("level")

	return cmd
}

func printUserPlan(diff *osuser.Diff) {
	fmt.Println("CURRENT STATE")
	if diff.UserExisted {
		fmt.Printf("  el usuario %q ya existe\n", diff.Username)
	} else {
		fmt.Printf("  el usuario %q no existe todavía\n", diff.Username)
	}

	fmt.Println("PROPOSED CHANGE")
	if !diff.UserExisted {
		fmt.Printf("  crear el usuario %q (home %s)\n", diff.Username, diff.HomeDir)
	}
	if len(diff.GroupsToAdd) > 0 {
		fmt.Printf("  agregar a los grupos: %s\n", strings.Join(diff.GroupsToAdd, ", "))
	}
	if diff.SudoRule != "" {
		fmt.Printf("  sudo (%s): %s\n", diff.SudoFilePath, diff.SudoRule)
	} else {
		fmt.Println("  sin privilegios de sudo (nivel solo_lectura)")
	}
	if diff.SSHKeyLine != "" {
		fmt.Println("  instalar clave SSH nueva en authorized_keys")
	} else if diff.SSHKeyAlreadyPresent {
		fmt.Println("  la clave SSH pedida ya estaba instalada — no cambia nada")
	}
}

func localUserListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Usuarios del sistema que Asterion administra en esta máquina",
		RunE: func(cmd *cobra.Command, args []string) error {
			users, err := osuser.ListManaged()
			if err != nil {
				return err
			}
			printJSON(users)
			return nil
		},
	}
}

func localUserRemoveCmd() *cobra.Command {
	var yes bool
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "remove <username>",
		Short: "Revierte exactamente lo que 'local user create' aplicó para este usuario",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]

			managed, err := osuser.GetManaged(username)
			if err != nil {
				return err
			}

			if !asJSON {
				fmt.Println("PROPOSED CHANGE")
				if !managed.Diff.UserExisted {
					fmt.Printf("  borrar el usuario %q (incluido su home)\n", username)
				} else {
					fmt.Printf("  quitar de %q solo lo que Asterion agregó: grupos %v, sudo (%s), clave SSH instalada\n",
						username, managed.Diff.GroupsToAdd, managed.Diff.SudoFilePath)
				}
			}

			if !yes && !asJSON {
				fmt.Print("\n¿Confirmar? [s/N]: ")
				answer := strings.ToLower(strings.TrimSpace(trimNewline(readLine())))
				if answer != "s" && answer != "si" && answer != "y" && answer != "yes" {
					fmt.Println("Cancelado — no se modificó nada.")
					return nil
				}
			}

			if err := safety.RequireSafeApply(safety.OSUserAdapter{}); err != nil {
				return err
			}

			diff := managed.Diff
			if err := osuser.Rollback(&diff); err != nil {
				return err
			}
			if err := osuser.RemoveManaged(username); err != nil {
				return err
			}

			if asJSON {
				printJSON(map[string]any{"username": username, "reverted": true})
				return nil
			}
			fmt.Printf("✓ %q revertido\n", username)
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "No pedir confirmación")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto (lo usa backend-core) — implica --yes")
	return cmd
}
