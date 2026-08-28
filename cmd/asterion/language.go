package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	langparser "github.com/Tarafagat/asterion-language/parser"
	langsemantic "github.com/Tarafagat/asterion-language/semantic"

	"asterion-core/internal/coreclient"
)

// languageCmd integra Asterion Language dentro de este CLI — ver el
// repo hermano github.com/Tarafagat/asterion-language (clonado al lado de
// este, igual que asterion-lab y asterion-plugin-contract). Solo 'check'
// existe hoy: lexa, parsea y valida referencias/capabilities — nunca toca
// infraestructura. 'plan'/'apply' esperan a que exista el traductor hacia
// LabSpec/APC/ProvisioningRequest (ver la propuesta de integración del
// audit de Asterion Language) — no están implementados todavía, a
// propósito, siguiendo el mismo criterio de fases que ya se usó para
// Asterion Lab y el Plugin Contract.
func languageCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "language",
		Short: "Asterion Language: valida código declarativo de infraestructura (fase check — plan/apply todavía no existen)",
	}
	root.AddCommand(languageCheckCmd())
	return root
}

func languageCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check <archivo.ast>",
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
