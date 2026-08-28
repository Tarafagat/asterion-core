// asterion es el CLI de Asterion. Habla con la API de Asterion (FastAPI)
// para todo lo que es estado/permisos/auditoría/sesión, y con asterion-core
// para consultas de capabilities de proveedor. Nunca llama un SDK de nube
// por su cuenta — esa lógica vive una sola vez, en asterion-core. Cómo la
// API arma o valida una sesión es un detalle interno suyo: el CLI solo
// conoce /auth/cli/* y una sesión genérica (ver internal/apiclient.Session).
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"asterion-core/internal/apiclient"
	"asterion-core/internal/cliconfig"
	"asterion-core/internal/coreclient"
)

var rootCmd = &cobra.Command{
	Use:   "asterion",
	Short: "CLI de Asterion — aprovisionamiento multi-nube",
	Long: "asterion es el cliente de línea de comandos de Asterion.\n" +
		"Describe, planifica, estima y aplica infraestructura contra tu cuenta de Asterion.",
}

func main() {
	rootCmd.AddCommand(
		whoamiCmd(),
		configCmd(),
		localCmd(),
		cloudCmd(),
		projectsCmd(),
		cloudAccountsCmd(),
		instancesCmd(),
		provisionCmd(),
		capabilitiesCmd(),
		providersCmd(),
		agentRunCmd(),
		agentCmd(),
		firewallCmd(),
		labCmd(),
		vmCmd(),
		containerCmd(),
		imagesCmd(),
		pluginsCmd(),
		languageCmd(),
	)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// apiToken devuelve un access token válido, renovándolo con el refresh
// token guardado si ya venció. Es lo que usa apiclient.Client como
// TokenFunc para cada llamada autenticada.
func apiToken() (string, error) {
	cfg, err := cliconfig.Load()
	if err != nil {
		return "", err
	}
	creds, err := cliconfig.LoadCredentials()
	if err != nil || creds.RefreshToken == "" {
		return "", fmt.Errorf("no hay sesión guardada, corré 'asterion cloud login' primero")
	}
	if time.Now().Before(creds.ExpiresAt.Add(-1 * time.Minute)) {
		return creds.AccessToken, nil
	}

	unauth := apiclient.NewUnauthenticated(cfg.APIBaseURL)
	session, err := unauth.RefreshSession(creds.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("la sesión venció y no se pudo renovar, corré 'asterion cloud login' de nuevo: %w", err)
	}
	creds.AccessToken = session.AccessToken
	creds.RefreshToken = session.RefreshToken
	creds.ExpiresAt = time.Now().Add(time.Duration(session.ExpiresIn) * time.Second)
	_ = cliconfig.SaveCredentials(creds)
	return creds.AccessToken, nil
}

func newAPIClient() (*apiclient.Client, error) {
	cfg, err := cliconfig.Load()
	if err != nil {
		return nil, err
	}
	return apiclient.New(cfg.APIBaseURL, apiToken), nil
}

func newCoreClient() (*coreclient.Client, error) {
	cfg, err := cliconfig.Load()
	if err != nil {
		return nil, err
	}
	return coreclient.New(cfg.CoreServiceURL), nil
}
