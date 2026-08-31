package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"asterion-core/internal/prereqs"
	"asterion-core/internal/upgrade"
)

// installCmd es el punto de entrada para "traer" partes del ecosistema
// Asterion a este workspace — hoy solo tiene un subcomando
// (prerequirements), pero queda como grupo separado por si en el futuro
// hace falta instalar alguna otra cosa que no sea "los repos hermanos".
func installCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "install",
		Short: "Instala partes del ecosistema Asterion en este workspace",
	}
	root.AddCommand(installPrerequirementsCmd())
	return root
}

// installPrerequirementsCmd clona los repos hermanos que asterion-core
// necesita, la primera vez. Es el complemento de "asterion upgrade": ese
// solo actualiza lo que YA está clonado (ver internal/upgrade), esto trae
// lo que todavía falta.
func installPrerequirementsCmd() *cobra.Command {
	var dir string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "prerequirements",
		Short: "Clona los repos hermanos que asterion-core necesita (lab, language, plugin-contract, shared)",
		Long: "Clona, al lado de asterion-core, los repos hermanos que hacen falta para\n" +
			"compilar con todos sus módulos (asterion-lab, asterion-language,\n" +
			"asterion-plugin-contract — los tres con 'replace ../X' en go.mod) y para que\n" +
			"backend-core arme su entorno Python (asterion-shared). Un repo que ya está\n" +
			"clonado se deja tal cual — correr esto de nuevo es seguro.\n\n" +
			"No tiene nada que ver con 'asterion plugin install' (eso instala plugins de\n" +
			"TERCEROS en la carpeta de config de Asterion, no el propio código fuente del\n" +
			"ecosistema) ni con 'asterion cloud install-agent' (eso conecta una instancia\n" +
			"a un proyecto de Asterion Cloud). Una vez clonados, 'asterion upgrade' los\n" +
			"mantiene al día.\n\n" +
			"Si clona algo nuevo, recompila 'asterion' solo — un binario ya compilado no\n" +
			"cambia porque aparezca código nuevo en disco, así que sin este paso los\n" +
			"comandos que dependen de lo recién clonado seguirían sin funcionar.",
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceDir := dir
			if workspaceDir == "" {
				var err error
				workspaceDir, err = upgrade.FindWorkspace("")
				if err != nil {
					return err
				}
			}
			results, err := prereqs.Install(workspaceDir)
			if err != nil {
				return err
			}

			anyCloned := false
			for _, r := range results {
				if r.Cloned {
					anyCloned = true
					break
				}
			}
			rebuild := ""
			if anyCloned {
				rebuild = rebuildSelf(workspaceDir)
			}

			if asJSON {
				payload := map[string]any{"workspace": workspaceDir, "results": results}
				if rebuild != "" {
					payload["rebuild"] = rebuild
				}
				printJSON(payload)
				return nil
			}
			fmt.Printf("Workspace: %s\n\n", workspaceDir)
			printPrereqResults(results)
			if rebuild != "" {
				fmt.Println()
				fmt.Println(rebuild)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "Carpeta del workspace (default: buscar subiendo desde el directorio actual)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

func printPrereqResults(results []prereqs.Result) {
	anyErr := false
	for _, r := range results {
		switch {
		case r.Error != "":
			anyErr = true
			fmt.Printf("✗ %s — %s\n", r.Name, r.Error)
		case r.Cloned:
			fmt.Printf("✓ %s — clonado\n", r.Name)
		default:
			fmt.Printf("✓ %s — ya estaba\n", r.Name)
		}
	}
	if anyErr {
		fmt.Println("\nAlgún repo no se pudo clonar — ver el detalle arriba.")
	}
}

// rebuildSelf recompila e instala 'asterion' de nuevo después de clonar
// repos hermanos nuevos — un binario ya compilado no cambia porque
// aparezca código fuente nuevo en disco, así que sin este paso los
// comandos que dependían de lo recién clonado (lab/vm/container/images/
// language, o plugin si lo que faltaba era asterion-plugin-contract)
// seguirían sin funcionar hasta la próxima recompilación manual.
//
// Solo actualiza $GOPATH/bin (equivalente a 'go install ./cmd/asterion',
// sin sudo) — a propósito no intenta copiar a /usr/local/bin como sí hace
// 'make install': esa copia puede pedir la contraseña de sudo de forma
// interactiva, y este comando no tiene cómo dársela. Si el workspace no
// tiene asterion-core (ej. --dir apuntando a otro lado sin ese repo), se
// omite en silencio — no es un error, ese workspace simplemente no es el
// que compila este binario.
func rebuildSelf(workspaceDir string) string {
	coreDir := filepath.Join(workspaceDir, "asterion-core")
	if _, err := os.Stat(filepath.Join(coreDir, "go.mod")); err != nil {
		return ""
	}
	if _, err := exec.LookPath("go"); err != nil {
		return "⚠ Se clonaron repos nuevos, pero no encontré 'go' en el PATH para recompilar — corré 'go install ./cmd/asterion' (o 'make install') a mano en asterion-core."
	}
	cmd := exec.Command("go", "install", "./cmd/asterion")
	cmd.Dir = coreDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		return fmt.Sprintf(
			"⚠ Se clonaron repos nuevos, pero no pude recompilar 'asterion' automáticamente — corré "+
				"'go install ./cmd/asterion' (o 'make install') a mano en asterion-core:\n%s", msg,
		)
	}
	return "✓ 'asterion' recompilado con los módulos nuevos ($GOPATH/bin actualizado — si también usás " +
		"/usr/local/bin, corré 'make install' o copialo a mano)."
}
