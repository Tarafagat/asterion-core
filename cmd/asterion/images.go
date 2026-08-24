package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"asterion-lab"
)

// imagesCmd administra el catálogo local de versiones de imágenes
// Docker — pensado para poder fijar/reproducir qué versión exacta
// (digest, no solo el tag) se usó al probar algo en el laboratorio.
// Independiente de cualquier laboratorio en particular: es un catálogo
// por usuario, no por lab.
func imagesCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "images",
		Short: "Catálogo local de versiones de imágenes Docker usadas en Asterion Lab",
	}
	root.AddCommand(imagesListCmd(), imagesPullCmd(), imagesRemoveCmd())
	return root
}

func imagesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista las imágenes Docker conocidas, con su digest real y cuándo se pidieron por última vez",
		RunE: func(cmd *cobra.Command, args []string) error {
			images, err := lab.ListDockerImages()
			if err != nil {
				return err
			}
			printJSON(images)
			return nil
		},
	}
}

func imagesPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull <imagen>",
		Short: "Descarga una imagen Docker y registra su versión (digest) en el catálogo local",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			digest, err := lab.PullDockerImage(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("✓ %q — %s\n", args[0], digest)
			return nil
		},
	}
}

func imagesRemoveCmd() *cobra.Command {
	var forget bool
	cmd := &cobra.Command{
		Use:   "remove <imagen>",
		Short: "Borra una imagen Docker (docker rmi) y la saca del catálogo local",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if forget {
				if err := lab.ForgetDockerImage(args[0]); err != nil {
					return err
				}
				fmt.Printf("✓ %q sacada del catálogo local (la imagen sigue en Docker)\n", args[0])
				return nil
			}
			if err := lab.RemoveDockerImage(args[0]); err != nil {
				return err
			}
			fmt.Printf("✓ %q borrada de Docker y del catálogo local\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&forget, "forget-only", false, "Sacar del catálogo local sin borrar la imagen de Docker")
	return cmd
}
