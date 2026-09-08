package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	langparser "github.com/Tarafagat/asterion-language/parser"
	"github.com/Tarafagat/asterion-language/providerspec"
	langsemantic "github.com/Tarafagat/asterion-language/semantic"

	"asterion-core/internal/coreclient"
)

// languageCmd integra Asterion Language dentro de este CLI — ver el
// repo hermano github.com/Tarafagat/asterion-language (clonado al lado de
// este, igual que asterion-lab y asterion-plugin-contract). 'check' lexa,
// parsea y valida referencias/capabilities sin tocar infraestructura;
// 'apply' además compila (providerspec.CompileInstances) y crea de
// verdad — hoy Provider.gcp.instance(...) y Provider.oci.instance(...),
// los únicos dos con un adapter real del otro lado (ver
// internal/adapters/gcp e internal/adapters/oci). 'plan' (un DAG
// real de múltiples recursos con orden de dependencias) sigue sin existir,
// a propósito — esta fase aplica un recurso a la vez, en el orden en que
// aparecen en el archivo.
func languageCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "language",
		Short: "Asterion Language: valida y aplica código declarativo de infraestructura (hoy: check + apply de instancias GCP)",
	}
	root.AddCommand(languageCheckCmd(), languageApplyCmd())
	return root
}

func languageCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check <archivo.asterion>",
		Short: "Lexa + parsea + valida referencias y capabilities — nunca ejecuta ni planifica nada",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLanguageCheck(args[0])
		},
	}
}

func runLanguageCheck(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no pude leer %s: %w", path, err)
	}

	prog, diags := langparser.Parse(src, path)
	if diags.HasErrors() {
		fmt.Print(diags.String())
		return fmt.Errorf("%s no compila", path)
	}

	resolver, source := resolveCapabilityResolver()
	fmt.Printf("(capabilities: %s)\n", source)

	semDiags := langsemantic.NewAnalyzer(resolver).Analyze(prog)
	if semDiags.HasErrors() {
		fmt.Print(semDiags.String())
		return fmt.Errorf("%s no pasó la validación semántica", path)
	}

	fmt.Printf("✓ %s — %d statement(s), sin errores\n", path, len(prog.Statements))
	return nil
}

func languageApplyCmd() *cobra.Command {
	var credentialsFile string
	var dryRun bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "apply <archivo.asterion>",
		Short: "Compila y crea de verdad los recursos que el archivo describe (hoy: instancias de GCP)",
		Long: "Corre check primero (nunca aplica un archivo que no compila o no pasa la\n" +
			"validación semántica) y después crea de verdad, vía el mismo servicio de\n" +
			"adapters que 'asterion providers'/'asterion capabilities' (localhost:8090 por\n" +
			"default) — nunca habla directo con el SDK de ningún proveedor. Aplica un\n" +
			"recurso a la vez, en el orden en que aparecen en el archivo — todavía no hay\n" +
			"un DAG de dependencias entre varios.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLanguageApply(args[0], credentialsFile, dryRun, asJSON)
		},
	}
	cmd.Flags().StringVar(&credentialsFile, "credentials-file", "",
		"Ruta al archivo de credenciales del proveedor — para GCP, el JSON de la service account tal cual; "+
			"para OCI, un JSON propio con user_ocid/tenancy_ocid/fingerprint/private_key — obligatorio salvo con --dry-run")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Solo compila y muestra qué se crearía, sin llamar a ningún proveedor")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

// buildCredentials arma el mapa de credenciales que espera cada adapter a
// partir del contenido crudo de --credentials-file — cada proveedor tiene
// su propia forma (GCP: un único JSON de service account que se manda tal
// cual bajo una sola clave; OCI: cuatro campos sueltos), no existe todavía
// una abstracción de credenciales multi-proveedor real de este lado.
func buildCredentials(provider string, raw []byte) (map[string]string, error) {
	switch provider {
	case "gcp":
		return map[string]string{"service_account_json": string(raw)}, nil
	case "oci":
		var creds map[string]string
		if err := json.Unmarshal(raw, &creds); err != nil {
			return nil, fmt.Errorf("--credentials-file para oci debe ser un JSON con user_ocid/tenancy_ocid/fingerprint/private_key: %w", err)
		}
		return creds, nil
	default:
		return nil, fmt.Errorf("todavía no sé qué forma de credenciales espera el proveedor %q", provider)
	}
}

func runLanguageApply(path, credentialsFile string, dryRun, asJSON bool) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no pude leer %s: %w", path, err)
	}

	prog, diags := langparser.Parse(src, path)
	if diags.HasErrors() {
		fmt.Print(diags.String())
		return fmt.Errorf("%s no compila", path)
	}

	resolver, source := resolveCapabilityResolver()
	if !asJSON {
		fmt.Printf("(capabilities: %s)\n", source)
	}
	semDiags := langsemantic.NewAnalyzer(resolver).Analyze(prog)
	if semDiags.HasErrors() {
		fmt.Print(semDiags.String())
		return fmt.Errorf("%s no pasó la validación semántica", path)
	}

	specs, compileDiags := providerspec.CompileInstances(prog)
	if compileDiags.HasErrors() {
		fmt.Print(compileDiags.String())
		return fmt.Errorf("%s no se pudo compilar a recursos aplicables", path)
	}
	if len(specs) == 0 {
		fmt.Println("Este archivo no declara ninguna instancia de un proveedor soportado todavía (hoy: Provider.gcp.instance / Provider.oci.instance) — nada que aplicar.")
		return nil
	}

	if dryRun {
		if asJSON {
			printJSON(specs)
			return nil
		}
		fmt.Printf("Se crearían %d recurso(s) (--dry-run, no se llamó a ningún proveedor):\n", len(specs))
		for _, spec := range specs {
			fmt.Printf("  %s (%s) — zona/región %s, shape %s, imagen %s\n", spec.Name, spec.Provider, spec.Region, spec.ShapeCode, spec.Image)
		}
		return nil
	}

	if credentialsFile == "" {
		return fmt.Errorf("falta --credentials-file (para GCP: el JSON de la service account) — hace falta para crear algo real, salvo con --dry-run")
	}
	credentialsRaw, err := os.ReadFile(credentialsFile)
	if err != nil {
		return fmt.Errorf("no pude leer %s: %w", credentialsFile, err)
	}

	client, err := newCoreClient()
	if err != nil {
		return err
	}

	type applyResult struct {
		Name     string         `json:"name"`
		Provider string         `json:"provider"`
		Result   map[string]any `json:"result,omitempty"`
		Error    string         `json:"error,omitempty"`
	}
	results := make([]applyResult, 0, len(specs))
	var firstErr error
	for _, spec := range specs {
		credentials, credErr := buildCredentials(spec.Provider, credentialsRaw)
		if credErr != nil {
			return credErr
		}
		body := map[string]any{
			"name":             spec.Name,
			"region":           spec.Region,
			"shape_code":       spec.ShapeCode,
			"image_id":         spec.Image,
			"network_ext_id":   spec.Network,
			"subnet_ext_id":    spec.Subnet,
			"assign_public_ip": spec.AssignPublicIP,
			"credentials":      credentials,
		}
		result, applyErr := client.CreateInstance(spec.Provider, body)
		r := applyResult{Name: spec.Name, Provider: spec.Provider, Result: result}
		if applyErr != nil {
			r.Error = applyErr.Error()
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", spec.Name, applyErr)
			}
			if !asJSON {
				fmt.Printf("✗ %s (%s) — %s\n", spec.Name, spec.Provider, applyErr)
			}
		} else if !asJSON {
			fmt.Printf("✓ %s (%s) — external_id=%v status=%v\n", spec.Name, spec.Provider, result["external_id"], result["status"])
		}
		results = append(results, r)
	}

	if asJSON {
		printJSON(results)
	}
	return firstErr
}

// resolveCapabilityResolver intenta hablarle al servicio real de adapters
// (cmd/asterion-core, el binario aparte — no confundir con 'local serve',
// que es el dashboard; ver internal/coreclient) para validar contra lo que
// ese servicio declara en vivo. Si no está corriendo, cae al snapshot
// estático de asterion-language — y lo dice explícitamente, nunca en
// silencio, para que un "no reconozco el provider X" no se confunda con
// un problema real del archivo cuando en realidad es que el servicio de
// adapters no está levantado.
func resolveCapabilityResolver() (langsemantic.CapabilityResolver, string) {
	client, err := newCoreClient()
	if err == nil {
		if providers, err := client.Providers(); err == nil {
			return &coreCapabilityResolver{client: client, providers: providers}, "en vivo, vía el servicio de adapters"
		}
	}
	return langsemantic.StaticCapabilityResolver{}, "snapshot estático — no pude conectar con el servicio de adapters (cmd/asterion-core, default :8090); los datos pueden estar desactualizados"
}

// coreCapabilityResolver responde langsemantic.CapabilityResolver
// consultando el servicio real de adapters por HTTP (mismo canal que ya
// usan 'asterion providers'/'asterion capabilities') — nunca construye su
// propio Registry en memoria, para no arriesgarse a que este CLI y el
// servicio en vivo terminen viendo cosas distintas.
type coreCapabilityResolver struct {
	client    *coreclient.Client
	providers []string
}

func (r *coreCapabilityResolver) Providers() []string { return r.providers }

func (r *coreCapabilityResolver) HasCapability(provider, capability string) bool {
	caps, err := r.client.Capabilities(provider)
	if err != nil {
		return false
	}
	return caps[capability]
}
