package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tarafagat/asterion-plugin-contract/sdk/go/scaffold"

	"asterion-core/internal/plugins"
)

// pluginsCmd administra el ciclo de vida de plugins de terceros: instalar
// (git clone + validar plugin.yaml), arrancar/parar (proceso propio, puerto
// propio), configurar (guardado cifrado en esta máquina), y conectar a un
// proyecto de Asterion Cloud. list/status siempre imprimen JSON — es lo que
// consume backend-core/app/plugin_bridge.py para mostrarlos en el
// dashboard, mismo patrón que 'asterion local status'/'doctor'.
func pluginsCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "plugin",
		Short: "Instala y administra plugins de terceros (integraciones que corren como proceso propio)",
	}
	root.AddCommand(
		pluginInstallCmd(),
		pluginListCmd(),
		pluginStatusCmd(),
		pluginStartCmd(),
		pluginStopCmd(),
		pluginRemoveCmd(),
		pluginConfigCmd(),
		pluginConnectCmd(),
		pluginInitCmd(),
		pluginValidateCmd(),
		pluginDevCmd(),
		pluginFromOpenAPICmd(),
	)
	return root
}

// pluginInitCmd genera el scaffold de un plugin nuevo que ya cumple el
// Asterion Plugin Contract — ver asterion-plugin-contract/sdk/go/scaffold.
func pluginInitCmd() *cobra.Command {
	var language, dir, description, author string
	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Genera la estructura de un plugin nuevo que ya cumple el Asterion Plugin Contract",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if language != "go" {
				return fmt.Errorf("--language %q todavía no tiene scaffold — hoy solo 'go'", language)
			}
			name := args[0]
			outDir := dir
			if outDir == "" {
				outDir = name
			}
			if err := scaffold.Generate(outDir, scaffold.Options{Name: name, Description: description, Author: author}); err != nil {
				return err
			}
			fmt.Printf("✓ Plugin %q generado en %s\n\n", name, outDir)
			fmt.Printf("cd %s\ngo build -o %s .\nasterion plugin validate .\n", outDir, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&language, "language", "go", "Lenguaje del scaffold (hoy: go)")
	cmd.Flags().StringVar(&dir, "dir", "", "Directorio de salida (default: el nombre del plugin)")
	cmd.Flags().StringVar(&description, "description", "", "Descripción para plugin.yaml/README.md")
	cmd.Flags().StringVar(&author, "author", "", "Autor para plugin.yaml")
	return cmd
}

// pluginValidateCmd corre el validador completo del contrato (estructura +
// que los archivos referenciados por ruta relativa existan) sobre un
// directorio local — no requiere que el plugin esté instalado.
func pluginValidateCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "validate <dir>",
		Short: "Valida que un plugin.yaml (y lo que referencia) cumple el Asterion Plugin Contract",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, err := plugins.ValidateManifestDir(args[0])
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(manifest)
				return nil
			}
			fmt.Printf("✓ %s cumple el Asterion Plugin Contract (%s)\n", args[0], manifest.ContractVersion)
			fmt.Printf("  %s — v%s\n", manifest.Name, manifest.Version)
			if len(manifest.Resources) > 0 {
				fmt.Printf("  resources: %d\n", len(manifest.Resources))
			}
			if len(manifest.Actions) > 0 {
				fmt.Printf("  actions: %d\n", len(manifest.Actions))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el manifiesto validado como JSON en vez de texto")
	return cmd
}

func pluginInstallCmd() *cobra.Command {
	var name string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "install <repo-url>",
		Short: "Instala un plugin clonando su repo (público o privado — usa tus credenciales de git)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			installed, err := plugins.Install(args[0], name)
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(installed)
				return nil
			}
			fmt.Printf("✓ Plugin %q instalado (%s)\n", installed.Name, installed.Manifest.Version)
			if installed.Manifest.Description != "" {
				fmt.Println("  " + installed.Manifest.Description)
			}
			fmt.Println("  id local:", installed.ExternalRef)
			if len(installed.Manifest.ConfigSchema) > 0 {
				fmt.Println("\nEste plugin necesita configuración antes de arrancar:")
				for _, f := range installed.Manifest.ConfigSchema {
					req := ""
					if f.Required {
						req = " (obligatorio)"
					}
					fmt.Printf("  - %s: %s%s\n", f.Key, f.Label, req)
				}
				fmt.Printf("\nConfigurala con: asterion plugin config set %s clave=valor\n", installed.Name)
			}
			fmt.Printf("\nArrancalo con: asterion plugin start %s\n", installed.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Nombre a usar si no coincide con el que se puede derivar de la URL")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el registro instalado como JSON en vez de texto (lo usa backend-core)")
	return cmd
}

func pluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista los plugins instalados en esta máquina",
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := plugins.List()
			if err != nil {
				return err
			}
			printJSON(list)
			return nil
		},
	}
}

func pluginStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <name>",
		Short: "Estado real de un plugin (reconcilia contra si el proceso sigue vivo)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			installed, err := plugins.Status(args[0])
			if err != nil {
				return err
			}
			printJSON(installed)
			return nil
		},
	}
}

func pluginStartCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "start <name>",
		Short: "Arranca el proceso del plugin en un puerto propio y espera a que responda health check",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			installed, err := plugins.Start(args[0])
			if asJSON {
				printJSON(installed)
				return err
			}
			if err != nil {
				return err
			}
			fmt.Printf("✓ %q corriendo — puerto %d, pid %d\n", installed.Name, installed.Port, installed.PID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el estado resultante como JSON en vez de texto")
	return cmd
}

func pluginStopCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "stop <name>",
		Short: "Detiene el proceso del plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := plugins.Stop(args[0])
			if err != nil {
				return err
			}
			if asJSON {
				installed, statusErr := plugins.Get(args[0])
				if statusErr != nil {
					return statusErr
				}
				printJSON(installed)
				return nil
			}
			fmt.Printf("✓ %q detenido\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el estado resultante como JSON en vez de texto")
	return cmd
}

func pluginRemoveCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Detiene (si está corriendo), borra el repo clonado y su configuración guardada",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := plugins.Uninstall(args[0]); err != nil {
				return err
			}
			if asJSON {
				printJSON(map[string]any{"removed": args[0]})
				return nil
			}
			fmt.Printf("✓ %q desinstalado\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

func pluginConfigCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "config",
		Short: "Configuración de un plugin (se guarda cifrada en esta máquina, nunca en el cliente)",
	}
	root.AddCommand(pluginConfigSetCmd(), pluginConfigShowCmd())
	return root
}

func pluginConfigSetCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "set <name> clave=valor [clave2=valor2 ...]",
		Short: "Guarda uno o más valores de configuración, cifrados",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			values := make(map[string]string, len(args)-1)
			for _, kv := range args[1:] {
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) != 2 || parts[0] == "" {
					return fmt.Errorf("formato inválido %q — usá clave=valor", kv)
				}
				values[parts[0]] = parts[1]
			}
			if err := plugins.SetConfig(name, values); err != nil {
				return err
			}
			if asJSON {
				printJSON(map[string]any{"updated": name, "fields": len(values)})
				return nil
			}
			fmt.Printf("✓ Config de %q actualizada (%d campo(s))\n", name, len(values))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

func pluginConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Muestra la config guardada (los campos marcados 'secret' en plugin.yaml salen enmascarados)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			installed, err := plugins.Get(args[0])
			if err != nil {
				return err
			}
			masked, err := plugins.GetConfigMasked(installed)
			if err != nil {
				return err
			}
			printJSON(masked)
			return nil
		},
	}
}

// pluginConnectCmd vincula un plugin instalado localmente a un proyecto de
// Asterion Cloud — mismo mecanismo que cloudConnectCmd para instancias
// (ver cloud.go:connectLocalInstance): external_ref como identidad
// estable, reusa la fila si ya estaba conectado en vez de duplicarla.
func pluginConnectCmd() *cobra.Command {
	var projectID int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "connect <name>",
		Short: "Vincula un plugin instalado localmente a un proyecto de Asterion Cloud",
		Long: "No duplica el plugin del lado de Cloud si ya estaba conectado: la identidad\n" +
			"real es el id local (external_ref) generado al instalarlo — Local y Cloud son\n" +
			"dos formas de ver el mismo plugin, nunca dos filas distintas.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			installed, err := plugins.Get(args[0])
			if err != nil {
				return err
			}

			client, err := newAPIClient()
			if err != nil {
				return err
			}
			result, err := client.ConnectLocalPlugin(projectID, map[string]any{
				"external_ref": installed.ExternalRef,
				"name":         installed.Name,
				"version":      installed.Manifest.Version,
				"source_repo":  installed.Manifest.Repo,
			})
			if err != nil {
				return err
			}

			already, _ := result["already_connected"].(bool)
			installed.ConnectedProjectID = projectID
			if err := plugins.Save(installed); err != nil {
				return err
			}

			if asJSON {
				printJSON(map[string]any{"installed": installed, "already_connected": already})
				return nil
			}
			if already {
				fmt.Println("✓ Ya estaba conectado a Asterion Cloud (se reusó el mismo registro, no se duplicó)")
			} else {
				fmt.Println("✓ Plugin conectado a Asterion Cloud")
			}
			fmt.Printf("\nPlugin:\n  %s (id local %s)\n\nCloud:\n  proyecto %d\n", installed.Name, installed.ExternalRef, projectID)
			return nil
		},
	}
	cmd.Flags().IntVar(&projectID, "project", 0, "ID del proyecto de Asterion Cloud")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}
