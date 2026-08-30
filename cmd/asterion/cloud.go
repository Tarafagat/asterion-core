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
	root.AddCommand(cloudLoginCmd(), cloudLogoutCmd(), cloudConnectCmd(), cloudDisconnectCmd(), cloudInstallAgentCmd(), cloudUninstallAgentCmd())
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

// confirmByEmailCode exige, antes de una acción que se pidió que no se
// pueda disparar con solo apretar el comando (hoy: desconectar una
// instancia o un plugin de Cloud), el mismo código de un solo uso por
// email que ya usa 'cloud login' — reusado tal cual
// (RequestLoginCode/VerifyLoginCode), sin ningún endpoint nuevo del
// backend. La sesión que devuelve VerifyLoginCode se descarta a
// propósito: ya hay una activa y vigente (la que hizo posible llegar
// hasta acá), no hace falta reemplazarla — lo único que importa es que
// falle si el código está mal, confirmando que quien está corriendo esto
// tiene acceso de VERDAD al correo de la cuenta ahora mismo, no solo una
// sesión de CLI que quedó abierta en esta máquina.
func confirmByEmailCode(cfg cliconfig.Config, email, action string) error {
	client := apiclient.NewUnauthenticated(cfg.APIBaseURL)
	if err := client.RequestLoginCode(email); err != nil {
		return fmt.Errorf("no pude pedir el código de verificación: %w", err)
	}
	fmt.Printf("Para confirmar %s, te mandamos un código a %s.\n", action, email)
	fmt.Print("Código: ")
	code := trimNewline(readLine())
	if code == "" {
		return fmt.Errorf("no se ingresó ningún código — no se hizo nada")
	}
	if _, err := client.VerifyLoginCode(email, code); err != nil {
		return fmt.Errorf("verificación fallida, no se hizo nada: %w", err)
	}
	return nil
}

// requireSessionEmail devuelve el email de la sesión activa (la que ya
// hizo falta para llegar hasta este comando) — es a esa dirección a la
// que se manda el código de confirmDisconnectByEmail, nunca una que se
// tipee en el momento: si alguien más tiene una sesión de CLI abierta en
// esta máquina, el código igual va al dueño real de la cuenta.
func requireSessionEmail() (cliconfig.Config, string, error) {
	cfg, err := cliconfig.Load()
	if err != nil {
		return cfg, "", err
	}
	creds, err := cliconfig.LoadCredentials()
	if err != nil || creds.Email == "" {
		return cfg, "", fmt.Errorf("no hay sesión guardada, corré 'asterion cloud login' primero")
	}
	return cfg, creds.Email, nil
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

// resolveProjectSlug devuelve el proyecto a usar (identificado por su
// slug, no por un id numérico — ver el README para el porqué): si el
// usuario ya pasó --project, se respeta tal cual. Si no, se listan sus
// proyectos y se le pide elegir uno por número; si todavía no tiene
// ninguno, se lo guía a crear uno ahí mismo (POST /projects) en vez de
// cortar el flujo con "falta --project" y mandarlo a buscar el slug a
// mano en el dashboard.
func resolveProjectSlug(client *apiclient.Client, flagProjectSlug string) (string, error) {
	if flagProjectSlug != "" {
		return flagProjectSlug, nil
	}

	projects, err := client.ListProjects()
	if err != nil {
		return "", fmt.Errorf("no pude listar tus proyectos: %w", err)
	}

	if len(projects) == 0 {
		fmt.Println("Todavía no tenés ningún proyecto en Asterion Cloud.")
		fmt.Print("¿Creamos uno ahora? [S/n]: ")
		answer := strings.ToLower(trimNewline(readLine()))
		if answer != "" && answer != "s" && answer != "si" && answer != "sí" {
			return "", fmt.Errorf("no hay proyecto para usar — creá uno con la web o pasá --project")
		}
		fmt.Print("Nombre del proyecto: ")
		name := trimNewline(readLine())
		if name == "" {
			return "", fmt.Errorf("el nombre del proyecto no puede estar vacío")
		}
		fmt.Print("Descripción (opcional): ")
		description := trimNewline(readLine())

		created, err := client.CreateProject(name, description)
		if err != nil {
			return "", fmt.Errorf("no pude crear el proyecto: %w", err)
		}
		slug, _ := created["slug"].(string)
		fmt.Printf("✓ Proyecto %q creado (%s)\n", name, slug)
		return slug, nil
	}

	// Mismo criterio que antes (más viejo primero) — created_at ocupa el
	// lugar que tenía el id numérico ascendente como proxy de orden de
	// creación, ahora que el id ya no es parte de lo que ve el usuario.
	sort.Slice(projects, func(i, j int) bool {
		iCreated, _ := projects[i]["created_at"].(string)
		jCreated, _ := projects[j]["created_at"].(string)
		return iCreated < jCreated
	})

	fmt.Println("Elegí a qué proyecto conectar esta instancia:")
	for i, p := range projects {
		slug, _ := p["slug"].(string)
		name, _ := p["name"].(string)
		fmt.Printf("  %d) %s (%s)\n", i+1, name, slug)
	}
	fmt.Print("Número (o el nombre del proyecto directamente): ")
	choice := trimNewline(readLine())
	if n, err := strconv.Atoi(choice); err == nil && n >= 1 && n <= len(projects) {
		slug, _ := projects[n-1]["slug"].(string)
		return slug, nil
	}
	// No matcheó como índice de la lista — se acepta también como el slug
	// tipeado directo (útil si el usuario ya sabía cuál quería).
	for _, p := range projects {
		if slug, _ := p["slug"].(string); slug == choice {
			return slug, nil
		}
	}
	return "", fmt.Errorf("no encontré el proyecto %q en la lista de arriba", choice)
}

// connectLocalInstance es el mecanismo compartido por `cloud connect` y
// `cloud install-agent`: vincula (o reusa, si ya estaba vinculada) una
// instancia local con un proyecto de Asterion Cloud, usando su id local
// como identidad estable (external_ref) — la misma instancia, dos modos
// de administración, nunca duplicada.
func connectLocalInstance(projectSlug string, instance localstore.Instance) (instanceID int, rawKey string, alreadyConnected bool, err error) {
	client, err := newAPIClient()
	if err != nil {
		return 0, "", false, err
	}

	result, err := client.ConnectLocalInstance(projectSlug, map[string]any{
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
	var projectSlug string
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
			resolvedProjectSlug, err := resolveProjectSlug(client, projectSlug)
			if err != nil {
				return err
			}

			fmt.Println("✓ Instancia local encontrada:", instance.Name)
			instanceID, rawKey, already, err := connectLocalInstance(resolvedProjectSlug, instance)
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

			fmt.Printf("\nInstancia:\n  %s (id local %s)\n\nCloud:\n  proyecto %s\n\nEstado:\n  ● Conectado (id remoto %d)\n", instance.Name, instance.ID, resolvedProjectSlug, instanceID)
			return nil
		},
	}
	cmd.Flags().StringVar(&projectSlug, "project", "", "Proyecto de Asterion Cloud (opcional — si se omite, se elige interactivamente)")
	return cmd
}

// cloudDisconnectCmd es lo que hace falta para poder reconectar una
// instancia a OTRO proyecto: mientras siga conectada a uno,
// connect_local_instance del backend rechaza con 409 cualquier intento de
// conectarla a un proyecto distinto (chequeo real, no solo de nombre — ver
// connectLocalInstance más arriba). --project es obligatorio a propósito,
// mismo criterio que 'cloud uninstall-agent': localstore.Instance no
// recuerda a qué proyecto está conectada (a diferencia de un plugin, ver
// plugin_disconnect), así que hace falta decirlo para poder resolver su id
// remoto antes de borrarlo del lado de Cloud.
func cloudDisconnectCmd() *cobra.Command {
	var projectSlug string
	cmd := &cobra.Command{
		Use:   "disconnect <nombre-local>",
		Short: "Desconecta una instancia de su proyecto de Asterion Cloud (libre para reconectarla a otro)",
		Long: "Borra la fila del lado de Cloud — no toca el perfil SSH local (para eso está\n" +
			"'asterion instances remove'). Después de esto, 'asterion cloud connect' puede\n" +
			"conectar la misma instancia al mismo proyecto de nuevo, o a uno distinto.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instance, err := localstore.Get(args[0])
			if err != nil {
				return err
			}

			cfg, email, err := requireSessionEmail()
			if err != nil {
				return err
			}
			if err := confirmByEmailCode(cfg, email, fmt.Sprintf("desconectar %q del proyecto %q", instance.Name, projectSlug)); err != nil {
				return err
			}

			client, err := newAPIClient()
			if err != nil {
				return err
			}

			// connect-local es idempotente: si ya estaba conectada a
			// --project, esto solo reusa la fila existente (sin duplicarla)
			// para obtener su id remoto — es la única forma de saber qué
			// borrar del lado de Cloud. Si en realidad está conectada a OTRO
			// proyecto, esto mismo lo va a decir con el 409 de siempre.
			remoteID, _, _, err := connectLocalInstance(projectSlug, instance)
			if err != nil {
				return fmt.Errorf("no encontré la conexión a Cloud de %q en el proyecto %q: %w", instance.Name, projectSlug, err)
			}

			if err := client.DeleteInstance(remoteID); err != nil {
				return err
			}
			fmt.Printf("✓ %q desconectada del proyecto %q — libre para reconectarla al mismo proyecto o a otro\n", instance.Name, projectSlug)
			return nil
		},
	}
	cmd.Flags().StringVar(&projectSlug, "project", "", "Proyecto de Asterion Cloud al que está conectada")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

func cloudInstallAgentCmd() *cobra.Command {
	var projectSlug string
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
			resolvedProjectSlug, err := resolveProjectSlug(client, projectSlug)
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
			instanceID, rawKey, already, err := connectLocalInstance(resolvedProjectSlug, instance)
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

			fmt.Printf("\nInstancia:\n  %s\n\nCloud:\n  proyecto %s (id remoto %d)\n\nEstado:\n  ● Conectado\n", instance.Name, resolvedProjectSlug, instanceID)
			return nil
		},
	}
	cmd.Flags().StringVar(&projectSlug, "project", "", "Proyecto de Asterion Cloud (opcional — si se omite, se elige interactivamente)")
	cmd.Flags().StringVar(&name, "name", "", "Nombre para esta máquina (default: el hostname)")
	return cmd
}

// cloudUninstallAgentCmd revierte install-agent: revoca las credenciales
// del lado de Cloud primero (para que dejen de aceptarse aunque el
// servicio local tarde en morir) y recién después para el servicio y
// borra la clave local — nunca al revés, evita una ventana donde el
// servicio siga corriendo con una clave que ya nadie va a revisar.
func cloudUninstallAgentCmd() *cobra.Command {
	var projectSlug string
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
			remoteID, _, _, err := connectLocalInstance(projectSlug, instance)
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
	cmd.Flags().StringVar(&projectSlug, "project", "", "Proyecto de Asterion Cloud al que estaba conectada")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}
