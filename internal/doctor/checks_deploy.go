package doctor

import (
	"context"
	"os"
	"path/filepath"

	"asterion-core/internal/pluginsvc"
)

// credentialProbe es una forma estándar de que un proveedor avise "ya
// tenés credenciales configuradas" — la variable de entorno que su propio
// SDK/CLI lee, o el archivo que su CLI oficial deja tras un login. Mirar
// estos lugares es exactamente lo que haría ese CLI si lo corrieras.
type credentialProbe struct {
	name    string
	envVars []string
	files   []string // relativos a $HOME
}

var credentialProbes = []credentialProbe{
	{name: "AWS", envVars: []string{"AWS_ACCESS_KEY_ID", "AWS_PROFILE"}, files: []string{".aws/credentials", ".aws/config"}},
	{name: "GCP", envVars: []string{"GOOGLE_APPLICATION_CREDENTIALS"}, files: []string{".config/gcloud/application_default_credentials.json"}},
	{name: "OCI", envVars: []string{"OCI_CLI_CONFIG_FILE"}, files: []string{".oci/config"}},
	{name: "Azure", envVars: []string{"AZURE_CLIENT_ID", "AZURE_SUBSCRIPTION_ID"}, files: []string{".azure/credentials"}},
	{name: "Vercel", envVars: []string{"VERCEL_TOKEN"}, files: []string{".local/share/com.vercel.cli/auth.json", "Library/Application Support/com.vercel.cli/auth.json"}},
}

// checkDeploymentTargets reporta, por cada destino, si hay con qué
// desplegar — no si el despliegue en sí va a funcionar (eso solo se sabe
// al intentarlo), sino la precondición concreta y barata de comprobar:
// ¿hay credenciales donde ese proveedor las busca?
func checkDeploymentTargets(ctx context.Context) []Check {
	out := []Check{
		{Section: "Deployment", Name: "Local", Severity: OK, Detail: "esta máquina — siempre disponible"},
	}

	if reason := pluginsvc.DockerUnavailableReason(); reason == "" {
		out = append(out, Check{Section: "Deployment", Name: "Docker", Severity: OK, Detail: "daemon alcanzable"})
	} else {
		out = append(out, Check{Section: "Deployment", Name: "Docker", Severity: Warn, Detail: reason})
	}

	home, _ := os.UserHomeDir()
	for _, p := range credentialProbes {
		if found, detail := p.detect(home); found {
			out = append(out, Check{Section: "Deployment", Name: p.name, Severity: OK, Detail: detail})
		} else {
			out = append(out, Check{Section: "Deployment", Name: p.name, Severity: NA, Detail: "sin credenciales configuradas en esta máquina"})
		}
	}
	return out
}

func (p credentialProbe) detect(home string) (bool, string) {
	for _, v := range p.envVars {
		if val := os.Getenv(v); val != "" {
			return true, "variable de entorno " + v + " presente"
		}
	}
	if home != "" {
		for _, f := range p.files {
			if _, err := os.Stat(filepath.Join(home, f)); err == nil {
				return true, "config encontrada en ~/" + f
			}
		}
	}
	return false, ""
}
