package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"asterion-core/internal/importer"
)

// importCmd es la puerta de entrada para un proyecto que YA EXISTE y no
// sabe nada de Asterion: en vez de "reescribí tu infraestructura", el
// camino es "corré esto y mirá qué detectó". Inspecciona lo que el
// proyecto ya tiene (package.json, requirements.txt, pyproject.toml,
// Dockerfile, docker-compose.yml, .env.example, prisma/schema.prisma,
// go.mod, Cargo.toml) y escribe un app.asterion de partida — nunca un
// plugin.yaml directo: el .asterion queda como la fuente editable, igual
// que 'plugin from-asterion' en todos lados en este CLI.
//
// Nada de lo detectado se asume con más confianza de la que tiene: cada
// dato generado dice de qué archivo salió, y lo que no se pudo inferir
// queda marcado con ⚠ en el archivo, no completado a ciegas — ver
// internal/importer, que es quien hace el trabajo real.
func importCmd() *cobra.Command {
	var out string
	var force bool
	cmd := &cobra.Command{
		Use:   "import [directorio]",
		Short: "Inspecciona un proyecto existente y genera un app.asterion de partida",
		Long: "Sin reescribir nada del proyecto: lee lo que ya declaró (Dockerfile, docker-compose.yml,\n" +
			"package.json, requirements.txt/pyproject.toml, .env.example, prisma/schema.prisma,\n" +
			"go.mod, Cargo.toml) y escribe un archivo .asterion al lado — esa es la entrega: un\n" +
			"app.asterion en Asterion Language, la misma fuente editable que ya usa 'plugin\n" +
			"from-asterion' en todos lados en este CLI, nunca un plugin.yaml generado directo.\n\n" +
			"Lo que se detecta con una fuente directa (un Dockerfile CMD, una imagen de\n" +
			"docker-compose, una variable de .env.example) queda marcado como tal; lo que es una\n" +
			"convención o una suposición razonable (el nombre del binario Go, un entrypoint de\n" +
			"Python por nombre de archivo) también se dice así — y lo que no se pudo encontrar en\n" +
			"absoluto queda como un placeholder con ⚠, nunca completado adivinando en silencio.\n\n" +
			"Después de revisarlo: 'asterion plugin from-asterion app.asterion --out .' lo compila a\n" +
			"un plugin.yaml real, y 'asterion plugin install . --link' lo prueba local. Para poder\n" +
			"instalarlo en otra máquina, esa carpeta se sube a un repo git propio — un plugin de\n" +
			"Asterion ES cualquier repo con un plugin.yaml válido en la raíz, no hace falta ningún\n" +
			"paso de \"publicar\" especial.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runImport(dir, out, force)
		},
	}
	cmd.Flags().StringVar(&out, "out", "app.asterion", "Dónde escribir el archivo generado")
	cmd.Flags().BoolVar(&force, "force", false, "Sobrescribir si --out ya existe")
	return cmd
}

func runImport(dir, out string, force bool) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%q no existe o no se puede leer: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q no es un directorio", dir)
	}

	if !force {
		if _, err := os.Stat(out); err == nil {
			return fmt.Errorf("%q ya existe — usá --force para sobrescribirlo, o --out para elegir otro nombre", out)
		}
	}

	scan, err := importer.Run(dir)
	if err != nil {
		return err
	}

	text := importer.Generate(scan)
	if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
		return err
	}

	printImportReport(dir, out, scan)
	return nil
}

func printImportReport(dir, out string, s *importer.Scan) {
	fmt.Printf("Inspeccioné %s\n\n", displayDir(dir))

	if len(s.FilesSeen) > 0 {
		fmt.Println("Archivos que usé:")
		for _, f := range s.FilesSeen {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println()
	}

	fmt.Println("Detectado:")
	fmt.Printf("  nombre:    %s  (%s)\n", s.Name.Value, origin(s.Name))
	if s.Language.Value != "" {
		fmt.Printf("  lenguaje:  %s %s  (%s)\n", s.Language.Value, s.LangVer.Value, origin(s.Language))
	} else {
		fmt.Printf("  lenguaje:  ⚠ no detectado\n")
	}
	if s.Start.Value != "" {
		fmt.Printf("  arranque:  %q  (%s)\n", s.Start.Value, origin(s.Start))
	} else {
		fmt.Printf("  arranque:  ⚠ no detectado — quedó un placeholder en el archivo\n")
	}
	fmt.Printf("  puerto:    %s  (%s)\n", s.Port.Value, origin(s.Port))
	fmt.Printf("  config:    %d variable(s)\n", len(s.Configs))
	if len(s.Services) > 0 {
		fmt.Printf("  servicios: ")
		for i, svc := range s.Services {
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Printf("%s (%s)", svc.Name, svc.Kind)
		}
		fmt.Println()
	}

	if len(s.Warnings) > 0 {
		fmt.Println("\nRevisar antes de compilar:")
		for _, w := range s.Warnings {
			fmt.Printf("  ⚠ %s\n", w)
		}
	}

	fmt.Printf("\n✓ %s escrito — esto es lo que se usa de acá en más (el .asterion queda como\n", out)
	fmt.Printf("  fuente editable; 'from-asterion' lo recompila cada vez, nunca se edita el\n")
	fmt.Printf("  plugin.yaml generado a mano).\n\n")
	fmt.Printf("Pasos siguientes:\n")
	fmt.Printf("  1. Revisá %s y completá lo marcado con ⚠.\n", out)
	fmt.Printf("  2. asterion plugin from-asterion %s --out .   # genera plugin.yaml acá mismo\n", out)
	fmt.Printf("  3. asterion plugin validate .                            # confirma que cumple el contrato\n")
	fmt.Printf("  4. asterion plugin install . --link                      # probarlo local, sin publicar nada\n")
	fmt.Printf("\nPara poder instalarlo en otra máquina (o compartirlo), subí esta carpeta a un\n")
	fmt.Printf("repo git propio y en el otro lado corré 'asterion plugin install <url-del-repo>'\n")
	fmt.Printf("— no hace falta ningún paso de \"publicar\" especial de Asterion, un plugin ES\n")
	fmt.Printf("cualquier repo con un plugin.yaml válido en la raíz.\n")
}

func origin(f importer.Finding) string {
	if f.Confidence == importer.Declared {
		return "detectado en " + f.Source
	}
	return "adivinado: " + f.Source
}

func displayDir(dir string) string {
	if dir == "." {
		wd, err := os.Getwd()
		if err == nil {
			return filepath.Base(wd)
		}
	}
	return dir
}
