package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tarafagat/asterion-plugin-contract/sdk/go/scaffold"

	"asterion-core/internal/apiclient"
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
		pluginFindCmd(),
		pluginStatusCmd(),
		pluginBuildCmd(),
		pluginStartCmd(),
		pluginStopCmd(),
		pluginRemoveCmd(),
		pluginConfigCmd(),
		pluginConnectCmd(),
		pluginDisconnectCmd(),
		pluginInitCmd(),
		pluginValidateCmd(),
		pluginDevCmd(),
		pluginFromOpenAPICmd(),
		pluginFromAsterionCmd(),
		pluginUpdateCmd(),
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
	var link bool
	cmd := &cobra.Command{
		Use:   "install <repo-url-o-carpeta>",
		Short: "Instala un plugin clonando su repo, o con --link, registra una carpeta local tal cual (sin copiarla)",
		Long: "Sin --link: clona <repo-url> (público o privado — usa tus credenciales de git) a\n" +
			"~/.config/asterion/plugins/repos/<name>.\n\n" +
			"Con --link: NO clona ni copia nada — registra la carpeta indicada tal cual está,\n" +
			"pensado para desarrollar (o simplemente probar) un plugin privado sin publicarlo a\n" +
			"ningún repo, ni siquiera tener uno local. Los cambios que hagas en el código se ven\n" +
			"la próxima vez que lo arranques (compilalo vos, Asterion nunca compila nada). Por\n" +
			"seguridad, 'asterion plugin remove' de un plugin --link NUNCA borra la carpeta —\n" +
			"solo lo desregistra.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			installed, err := plugins.Install(args[0], name, link)
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(installed)
				return nil
			}
			verb := "instalado"
			if link {
				verb = "vinculado (sin copiar)"
			}
			fmt.Printf("✓ Plugin %q %s (%s)\n", installed.Name, verb, installed.Manifest.Version)
			if installed.Manifest.Description != "" {
				fmt.Println("  " + installed.Manifest.Description)
			}
			fmt.Println("  id local:", installed.ExternalRef)
			if link {
				fmt.Println("  carpeta:", installed.Dir)
			}
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
	cmd.Flags().StringVar(&name, "name", "", "Nombre a usar si no coincide con el que se puede derivar de la URL/plugin.yaml")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el registro instalado como JSON en vez de texto (lo usa backend-core)")
	cmd.Flags().BoolVar(&link, "link", false, "Registrar una carpeta local tal cual, sin clonar ni copiar — para plugins privados en desarrollo")
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

// pluginFindCmd es la versión legible-por-humano de 'plugin list' (que
// siempre imprime JSON, a propósito: es lo que consume backend-core para el
// dashboard). Sirve para revisar rápido en la terminal qué hay instalado
// antes de un 'plugin connect' — incluye cualquier plugin de esta máquina,
// propio o de terceros, publicado o instalado con --link para desarrollo
// privado: plugins.List() no distingue el origen, solo lee el estado local.
func pluginFindCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "find",
		Short: "Lista, en formato legible, los plugins instalados en esta máquina",
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := plugins.List()
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Println("No hay ningún plugin instalado en esta máquina — 'asterion plugin install <repo-url>' para sumar uno.")
				return nil
			}
			printInstalledPlugins(list)
			return nil
		},
	}
}

func printInstalledPlugins(list []plugins.Installed) {
	for i, p := range list {
		desc := p.Manifest.Description
		if desc == "" {
			desc = "(sin descripción)"
		}
		fmt.Printf("%d) %s v%s — %s\n", i+1, p.Name, p.Manifest.Version, desc)
		fmt.Printf("   estado: %s", p.Status)
		if p.ConnectedProjectSlug != "" {
			fmt.Printf(" · conectado al proyecto %s de Asterion Cloud\n", p.ConnectedProjectSlug)
		} else {
			fmt.Println(" · no conectado a ningún proyecto de Asterion Cloud")
		}
	}
}

// resolveInstalledPlugin devuelve el plugin a usar: si ya se pasó un
// nombre, se respeta tal cual (plugins.Get valida que exista). Si no, se
// listan los plugins instalados en esta máquina para elegir uno por
// número o por nombre — mismo criterio que resolveProjectSlug en cloud.go
// para no obligar a memorizar el nombre exacto de antemano.
func resolveInstalledPlugin(name string) (plugins.Installed, error) {
	if name != "" {
		return plugins.Get(name)
	}

	list, err := plugins.List()
	if err != nil {
		return plugins.Installed{}, err
	}
	if len(list) == 0 {
		return plugins.Installed{}, fmt.Errorf("no hay ningún plugin instalado en esta máquina — 'asterion plugin install <repo-url>' primero")
	}

	fmt.Println("Elegí qué plugin conectar:")
	printInstalledPlugins(list)
	fmt.Print("Número (o el nombre directamente): ")
	choice := trimNewline(readLine())
	if n, err := strconv.Atoi(choice); err == nil {
		if n >= 1 && n <= len(list) {
			return list[n-1], nil
		}
		return plugins.Installed{}, fmt.Errorf("no hay ningún plugin en la posición %d", n)
	}
	for _, p := range list {
		if p.Name == choice {
			return p, nil
		}
	}
	return plugins.Installed{}, fmt.Errorf("no encontré un plugin llamado %q", choice)
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

func pluginBuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build <name>",
		Short: "Compila el binario (y el frontend, si tiene) de un plugin ya instalado",
		Long: "Paso explícito a propósito: Asterion nunca ejecuta código de un plugin de terceros por su\n" +
			"cuenta, ni siquiera para compilarlo — este comando es ese pedido puntual del operador, no algo\n" +
			"que 'install'/'start' disparen solos. Hoy solo sabe compilar plugins con language.name=\"go\"\n" +
			"en su plugin.yaml (los dos oficiales, asterion-mail-plugin-basic y asterion-firewall-analysis,\n" +
			"lo son) — si tiene frontend/package.json, también corre 'pnpm install && pnpm build' ahí.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Compilando %q...\n", args[0])
			log, err := plugins.Build(args[0])
			if log != "" {
				fmt.Println(log)
			}
			if err != nil {
				return err
			}
			fmt.Printf("✓ Listo — 'asterion plugin start %s' ya debería funcionar\n", args[0])
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

// connectResult es el resultado de conectar UN plugin instalado a un
// proyecto — una fila del reporte que arma --all. Vive acá (no en
// internal/plugins) porque conectar de por sí ya cruza dos paquetes
// (plugins.Save + apiclient.Client.ConnectLocalPlugin), igual que ya
// hacía el código de un solo plugin antes de esto.
type connectResult struct {
	Installed        plugins.Installed `json:"installed"`
	AlreadyConnected bool              `json:"already_connected"`
	Error            string            `json:"error,omitempty"`
}

func connectOne(client *apiclient.Client, projectSlug string, installed plugins.Installed) connectResult {
	result, err := client.ConnectLocalPlugin(projectSlug, map[string]any{
		"external_ref": installed.ExternalRef,
		"name":         installed.Name,
		"version":      installed.Manifest.Version,
		"source_repo":  installed.Manifest.Repo,
	})
	if err != nil {
		return connectResult{Installed: installed, Error: err.Error()}
	}

	already, _ := result["already_connected"].(bool)
	installed.ConnectedProjectSlug = projectSlug
	if err := plugins.Save(installed); err != nil {
		return connectResult{Installed: installed, Error: err.Error()}
	}
	return connectResult{Installed: installed, AlreadyConnected: already}
}

func printConnectResult(r connectResult) {
	if r.Error != "" {
		fmt.Printf("✗ %s — %s\n", r.Installed.Name, r.Error)
		return
	}
	if r.AlreadyConnected {
		fmt.Printf("✓ %s — ya estaba conectado (se reusó el mismo registro, no se duplicó)\n", r.Installed.Name)
	} else {
		fmt.Printf("✓ %s — conectado\n", r.Installed.Name)
	}
}

// pluginConnectCmd vincula un plugin instalado localmente a un proyecto de
// Asterion Cloud — mismo mecanismo que cloudConnectCmd para instancias
// (ver cloud.go:connectLocalInstance): external_ref como identidad
// estable, reusa la fila si ya estaba conectado en vez de duplicarla.
// Tanto <name> como --project son opcionales: sin ellos, el comando lista
// los plugins instalados y los proyectos disponibles para elegir
// interactivamente (mismo criterio que 'asterion cloud connect'). Con
// --all, conecta TODOS los plugins instalados al mismo proyecto (resuelto
// una sola vez, antes de recorrerlos) — un plugin que falla no corta el
// resto, mismo criterio que 'plugin update --all'.
func pluginConnectCmd() *cobra.Command {
	var projectSlug string
	var asJSON bool
	var all bool
	cmd := &cobra.Command{
		Use:   "connect [name]",
		Short: "Vincula uno (o, con --all, todos) los plugins instalados localmente a un proyecto de Asterion Cloud",
		Long: "No duplica el plugin del lado de Cloud si ya estaba conectado: la identidad\n" +
			"real es el id local (external_ref) generado al instalarlo — Local y Cloud son\n" +
			"dos formas de ver el mismo plugin, nunca dos filas distintas.\n\n" +
			"Si se omite [name] (y no se pasa --all), lista los plugins instalados en esta\n" +
			"máquina para elegir uno (igual que 'asterion plugin find'). Si se omite\n" +
			"--project, lista tus proyectos de Asterion Cloud para elegir uno, u ofrece crear\n" +
			"uno nuevo si todavía no tenés ninguno — con --all, ese proyecto se resuelve una\n" +
			"sola vez y se usa para todos.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if all && name != "" {
				return fmt.Errorf("pasá un nombre o --all, no los dos")
			}

			client, err := newAPIClient()
			if err != nil {
				return err
			}

			if all {
				list, err := plugins.List()
				if err != nil {
					return err
				}
				if len(list) == 0 {
					return fmt.Errorf("no hay ningún plugin instalado en esta máquina — 'asterion plugin install <repo-url>' primero")
				}
				resolvedProjectSlug, err := resolveProjectSlug(client, projectSlug)
				if err != nil {
					return err
				}
				results := make([]connectResult, 0, len(list))
				for _, installed := range list {
					results = append(results, connectOne(client, resolvedProjectSlug, installed))
				}
				if asJSON {
					printJSON(map[string]any{"project": resolvedProjectSlug, "results": results})
					return nil
				}
				fmt.Printf("Proyecto: %s\n\n", resolvedProjectSlug)
				anyErr := false
				for _, r := range results {
					printConnectResult(r)
					if r.Error != "" {
						anyErr = true
					}
				}
				if anyErr {
					return fmt.Errorf("algún plugin no se pudo conectar — ver el detalle arriba")
				}
				return nil
			}

			installed, err := resolveInstalledPlugin(name)
			if err != nil {
				return err
			}
			resolvedProjectSlug, err := resolveProjectSlug(client, projectSlug)
			if err != nil {
				return err
			}
			result := connectOne(client, resolvedProjectSlug, installed)

			if asJSON {
				printJSON(result)
				if result.Error != "" {
					return fmt.Errorf("%s", result.Error)
				}
				return nil
			}
			if result.Error != "" {
				return fmt.Errorf("%s", result.Error)
			}
			if result.AlreadyConnected {
				fmt.Println("✓ Ya estaba conectado a Asterion Cloud (se reusó el mismo registro, no se duplicó)")
			} else {
				fmt.Println("✓ Plugin conectado a Asterion Cloud")
			}
			fmt.Printf("\nPlugin:\n  %s (id local %s)\n\nCloud:\n  proyecto %s\n", installed.Name, installed.ExternalRef, resolvedProjectSlug)
			return nil
		},
	}
	cmd.Flags().StringVar(&projectSlug, "project", "", "Proyecto de Asterion Cloud (opcional — si se omite, se elige interactivamente)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	cmd.Flags().BoolVar(&all, "all", false, "Conectar todos los plugins instalados al mismo proyecto")
	return cmd
}

// pluginDisconnectCmd es lo que hace falta para poder reconectar un plugin
// a OTRO proyecto: mientras siga 'connected', connect_local_plugin del
// backend rechaza con 409 cualquier intento de conectarlo a un proyecto
// distinto (chequeo real por status, no solo de nombre — el backend tenía
// un bug real acá, corregido junto con esto: un plugin desconectado
// seguía bloqueando para siempre la reconexión a otro proyecto porque la
// fila nunca perdía su project_id viejo). A diferencia de
// 'cloud disconnect' (instancias), acá --project es opcional: el propio
// 'plugin connect' ya guarda a qué proyecto quedó conectado cada plugin en
// state.json, así que por default se usa ese.
func pluginDisconnectCmd() *cobra.Command {
	var projectSlug string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "disconnect <name>",
		Short: "Desconecta un plugin de su proyecto de Asterion Cloud (libre para reconectarlo a otro)",
		Long: "El backend no borra el registro, lo marca 'disconnected' (para no perder el\n" +
			"historial) — pero eso alcanza para que 'asterion plugin connect' pueda volver a\n" +
			"conectarlo, al mismo proyecto o a uno distinto. No toca nada de la instalación\n" +
			"local (para eso está 'asterion plugin remove').",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			installed, err := plugins.Get(args[0])
			if err != nil {
				return err
			}

			resolvedProjectSlug := projectSlug
			if resolvedProjectSlug == "" {
				resolvedProjectSlug = installed.ConnectedProjectSlug
			}
			if resolvedProjectSlug == "" {
				return fmt.Errorf("no sé a qué proyecto está conectado %q — pasá --project", installed.Name)
			}

			client, err := newAPIClient()
			if err != nil {
				return err
			}

			// connect-local es idempotente: resuelve el id remoto del plugin
			// en ese proyecto sin duplicar nada — es la única forma de saber
			// qué desconectar del lado de Cloud. Si en realidad está
			// conectado a OTRO proyecto, esto mismo lo va a decir con el 409
			// de siempre.
			result, err := client.ConnectLocalPlugin(resolvedProjectSlug, map[string]any{
				"external_ref": installed.ExternalRef,
				"name":         installed.Name,
				"version":      installed.Manifest.Version,
				"source_repo":  installed.Manifest.Repo,
			})
			if err != nil {
				return fmt.Errorf("no encontré la conexión a Cloud de %q en el proyecto %q: %w", installed.Name, resolvedProjectSlug, err)
			}
			pluginMap, _ := result["plugin"].(map[string]any)
			idFloat, _ := pluginMap["id"].(float64)

			if err := client.DisconnectPlugin(resolvedProjectSlug, int(idFloat)); err != nil {
				return err
			}

			installed.ConnectedProjectSlug = ""
			if err := plugins.Save(installed); err != nil {
				return err
			}

			if asJSON {
				printJSON(map[string]any{"disconnected": installed.Name, "project": resolvedProjectSlug})
				return nil
			}
			fmt.Printf("✓ %q desconectado del proyecto %q — libre para reconectarlo al mismo proyecto o a otro\n", installed.Name, resolvedProjectSlug)
			return nil
		},
	}
	cmd.Flags().StringVar(&projectSlug, "project", "", "Proyecto de Asterion Cloud (opcional — por default el que ya tenía guardado en 'plugin connect')")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}
