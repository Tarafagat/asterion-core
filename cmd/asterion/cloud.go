package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"asterion-core/internal/apiclient"
	"asterion-core/internal/cliconfig"
	"asterion-core/internal/localstore"
)

// cloudCmd agrupa todo lo que implica Asterion Cloud: iniciar sesión,
// cerrar sesión, y vincular una instancia administrada localmente con un
// proyecto de Asterion Cloud sin duplicarla.
func cloudCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "cloud",
		Short: "Asterion Cloud: sesión, y vincular instancias locales a un proyecto",
	}
	root.AddCommand(cloudLoginCmd(), cloudLogoutCmd(), cloudConnectCmd(), cloudInstallAgentCmd(), cloudUninstallAgentCmd())
	return root
}

func cloudLoginCmd() *cobra.Command {
	var email, apiBaseURL string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Inicia sesión en Asterion Cloud con un código de un solo uso enviado por email",
		Long: "No usa contraseña: te mandamos un código de 6 dígitos por correo a la dirección\n" +
			"de tu cuenta de Asterion, lo pegás acá, y listo.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return err
			}
			if apiBaseURL != "" {
				cfg.APIBaseURL = apiBaseURL
			}
			if err := cliconfig.Save(cfg); err != nil {
				return err
			}

			if email == "" {
				fmt.Print("Email: ")
				email = trimNewline(readLine())
			}

			client := apiclient.NewUnauthenticated(cfg.APIBaseURL)
			if err := client.RequestLoginCode(email); err != nil {
				return err
			}
			fmt.Println("Te mandamos un código a tu correo si esa dirección tiene una cuenta de Asterion.")
			fmt.Print("Código: ")
			code := trimNewline(readLine())

			session, err := client.VerifyLoginCode(email, code)
			if err != nil {
				return err
			}

			if err := cliconfig.SaveCredentials(cliconfig.Credentials{
				Email:        email,
				AccessToken:  session.AccessToken,
				RefreshToken: session.RefreshToken,
				ExpiresAt:    time.Now().Add(time.Duration(session.ExpiresIn) * time.Second),
			}); err != nil {
				return err
			}

			fmt.Printf("Sesión iniciada como %s (API: %s)\n", email, cfg.APIBaseURL)
			return nil
		},
	}

	cmd.Flags().StringVar(&email, "email", "", "Email de tu cuenta de Asterion")
	cmd.Flags().StringVar(&apiBaseURL, "api-url", "", "URL base de la API de Asterion (default http://localhost:8000)")
	return cmd
}

func cloudLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Cierra la sesión de Asterion Cloud en esta máquina",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cliconfig.SaveCredentials(cliconfig.Credentials{}); err != nil {
				return err
			}
			fmt.Println("Sesión cerrada.")
			return nil
		},
	}
}

// resolveProjectID devuelve el proyecto a usar: si el usuario ya pasó
// --project, se respeta tal cual. Si no, se listan sus proyectos y se le
// pide elegir uno por número; si todavía no tiene ninguno, se lo guía a
// crear uno ahí mismo (POST /projects) en vez de cortar el flujo con
// "falta --project" y mandarlo a buscar el ID a mano en el dashboard.
func resolveProjectID(client *apiclient.Client, flagProjectID int) (int, error) {
	if flagProjectID != 0 {
		return flagProjectID, nil
	}

	projects, err := client.ListProjects()
	if err != nil {
		return 0, fmt.Errorf("no pude listar tus proyectos: %w", err)
	}

	if len(projects) == 0 {
		fmt.Println("Todavía no tenés ningún proyecto en Asterion Cloud.")
		fmt.Print("¿Creamos uno ahora? [S/n]: ")
		answer := strings.ToLower(trimNewline(readLine()))
		if answer != "" && answer != "s" && answer != "si" && answer != "sí" {
			return 0, fmt.Errorf("no hay proyecto para usar — creá uno con la web o pasá --project")
		}
		fmt.Print("Nombre del proyecto: ")
		name := trimNewline(readLine())
		if name == "" {
			return 0, fmt.Errorf("el nombre del proyecto no puede estar vacío")
		}
		fmt.Print("Descripción (opcional): ")
		description := trimNewline(readLine())

		created, err := client.CreateProject(name, description)
		if err != nil {
			return 0, fmt.Errorf("no pude crear el proyecto: %w", err)
		}
		idFloat, _ := created["id"].(float64)
		fmt.Printf("✓ Proyecto %q creado (id %d)\n", name, int(idFloat))
		return int(idFloat), nil
	}

	sort.Slice(projects, func(i, j int) bool {
		iID, _ := projects[i]["id"].(float64)
		jID, _ := projects[j]["id"].(float64)
		return iID < jID
	})

	fmt.Println("Elegí a qué proyecto conectar esta instancia:")
	for i, p := range projects {
		idFloat, _ := p["id"].(float64)
		name, _ := p["name"].(string)
		fmt.Printf("  %d) %s (id %d)\n", i+1, name, int(idFloat))
	}
	fmt.Print("Número (o el ID directamente): ")
	choice := trimNewline(readLine())
	n, err := strconv.Atoi(choice)
	if err != nil {
		return 0, fmt.Errorf("respuesta inválida: %q", choice)
	}
	if n >= 1 && n <= len(projects) {
		idFloat, _ := projects[n-1]["id"].(float64)
		return int(idFloat), nil
	}
	// No matcheó como índice de la lista — se acepta también como un ID
	// de proyecto tipeado directo (útil si el usuario ya sabía cuál quería).
	for _, p := range projects {
		idFloat, _ := p["id"].(float64)
		if int(idFloat) == n {
			return n, nil
		}
	}
	return 0, fmt.Errorf("no encontré el proyecto %d en la lista de arriba", n)
}

// connectLocalInstance es el mecanismo compartido por `cloud connect` y
// `cloud install-agent`: vincula (o reusa, si ya estaba vinculada) una
// instancia local con un proyecto de Asterion Cloud, usando su id local
// como identidad estable (external_ref) — la misma instancia, dos modos
// de administración, nunca duplicada.
func connectLocalInstance(projectID int, instance localstore.Instance) (instanceID int, rawKey string, alreadyConnected bool, err error) {
	client, err := newAPIClient()
	if err != nil {
		return 0, "", false, err
	}

	result, err := client.ConnectLocalInstance(projectID, map[string]any{
		"external_ref": instance.ID,
		"name":         instance.Name,
		"cpu_cores":    1,
		"ram_gb":       1,
		"storage_gb":   10,
		"os":           "linux",
		"region":       "local",
	})
	if err != nil {
		return 0, "", false, err
	}

	already, _ := result["already_connected"].(bool)
	instanceMap, _ := result["instance"].(map[string]any)
	idFloat, _ := instanceMap["id"].(float64)
	key, _ := result["raw_key"].(string)

	return int(idFloat), key, already, nil
}

func cloudConnectCmd() *cobra.Command {
	var projectID int
	cmd := &cobra.Command{
		Use:   "connect <nombre-local>",
		Short: "Vincula una instancia de tu inventario local a un proyecto de Asterion Cloud",
		Long: "No crea una instancia nueva del lado de Cloud si esta ya estaba conectada: la\n" +
			"identidad real del recurso es el id local (external_ref), Local y Cloud son dos\n" +
			"modos de administración sobre la misma instancia.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instance, err := localstore.Get(args[0])
			if err != nil {
				return err
			}

			client, err := newAPIClient()
			if err != nil {
				return err
			}
			resolvedProjectID, err := resolveProjectID(client, projectID)
			if err != nil {
				return err
			}

			fmt.Println("✓ Instancia local encontrada:", instance.Name)
			instanceID, rawKey, already, err := connectLocalInstance(resolvedProjectID, instance)
			if err != nil {
				return err
			}

			if already {
				fmt.Println("✓ Ya estaba conectada a Asterion Cloud (se reusó la misma instancia, no se duplicó)")
			} else {
				fmt.Println("✓ Token de enrolamiento generado")
				fmt.Println("✓ Instancia conectada a Asterion Cloud")
				if err := saveAgentKey(instance.ID, rawKey); err != nil {
					return err
				}
				fmt.Println("  Clave del agente guardada localmente — corré 'asterion agent-run --local " + instance.ID + "' para empezar a reportar métricas.")
			}

			fmt.Printf("\nInstancia:\n  %s (id local %s)\n\nCloud:\n  proyecto %d\n\nEstado:\n  ● Conectado (id remoto %d)\n", instance.Name, instance.ID, resolvedProjectID, instanceID)
			return nil
		},
	}
	cmd.Flags().IntVar(&projectID, "project", 0, "ID del proyecto de Asterion Cloud (opcional — si se omite, se elige interactivamente)")
	return cmd
}

func cloudInstallAgentCmd() *cobra.Command {
	var projectID int
	var name string
	cmd := &cobra.Command{
		Use:   "install-agent",
		Short: "Registra ESTA máquina como una instancia del proyecto y deja el agente corriendo",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				hostname, _ := os.Hostname()
				name = hostname
			}

			client, err := newAPIClient()
			if err != nil {
				return err
			}
			resolvedProjectID, err := resolveProjectID(client, projectID)
			if err != nil {
				return err
			}

			// Si esta máquina ya se había registrado antes, reusamos el
			// mismo perfil local en vez de crear uno nuevo cada vez.
			selfName := "self-" + name
			instance, err := localstore.Get(selfName)
			if err != nil {
				instance = localstore.Instance{ID: localstore.NewID(), Name: selfName, Host: "localhost", Port: 22, User: os.Getenv("USER")}
				if err := localstore.Add(instance); err != nil {
					return err
				}
			}

			fmt.Println("✓ Instancia encontrada:", instance.Name)
			instanceID, rawKey, already, err := connectLocalInstance(resolvedProjectID, instance)
			if err != nil {
				return err
			}
			if already {
				fmt.Println("✓ Ya estaba conectada a Asterion Cloud")
			} else {
				fmt.Println("✓ Token de enrolamiento generado")
				fmt.Println("✓ Instancia conectada a Asterion Cloud")
				if err := saveAgentKey(instance.ID, rawKey); err != nil {
					return err
				}
			}

			if err := installAgentService(instance.ID); err != nil {
				fmt.Println("⚠ No se pudo instalar el servicio del agente automáticamente:", err)
				fmt.Printf("  Corré manualmente: asterion agent-run --local %s\n", instance.ID)
			} else {
				fmt.Println("✓ Agente instalado y corriendo (systemd --user)")
			}

			fmt.Printf("\nInstancia:\n  %s\n\nCloud:\n  proyecto %d (id remoto %d)\n\nEstado:\n  ● Conectado\n", instance.Name, resolvedProjectID, instanceID)
			return nil
		},
	}
	cmd.Flags().IntVar(&projectID, "project", 0, "ID del proyecto de Asterion Cloud (opcional — si se omite, se elige interactivamente)")
	cmd.Flags().StringVar(&name, "name", "", "Nombre para esta máquina (default: el hostname)")
	return cmd
}

// cloudUninstallAgentCmd revierte install-agent: revoca las credenciales
// del lado de Cloud primero (para que dejen de aceptarse aunque el
// servicio local tarde en morir) y recién después para el servicio y
// borra la clave local — nunca al revés, evita una ventana donde el
// servicio siga corriendo con una clave que ya nadie va a revisar.
func cloudUninstallAgentCmd() *cobra.Command {
	var projectID int
	cmd := &cobra.Command{
		Use:   "uninstall-agent <nombre-local>",
		Short: "Revoca el agente de una instancia y desinstala el servicio local",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instance, err := localstore.Get(args[0])
			if err != nil {
				return err
			}

			client, err := newAPIClient()
			if err != nil {
				return err
			}

			// connect-local es idempotente: si ya estaba conectada, esto solo
			// reusa la fila existente (sin duplicarla) para obtener su id
			// remoto — es la única forma de saber qué revocar del lado de Cloud.
			remoteID, _, _, err := connectLocalInstance(projectID, instance)
			if err != nil {
				return fmt.Errorf("no encontré la conexión a Cloud de %q: %w", instance.Name, err)
			}

			if err := client.RevokeAgent(remoteID); err != nil {
				return err
			}
			fmt.Println("✓ Credenciales del agente revocadas en Asterion Cloud")

			if err := uninstallAgentService(instance.ID); err != nil {
				fmt.Println("⚠ No se pudo desinstalar el servicio local automáticamente:", err)
			} else {
				fmt.Println("✓ Servicio local desinstalado")
			}

			removeAgentKey(instance.ID)
			fmt.Println("✓ Clave local del agente eliminada")
			return nil
		},
	}
	cmd.Flags().IntVar(&projectID, "project", 0, "ID del proyecto de Asterion Cloud al que estaba conectada")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}
