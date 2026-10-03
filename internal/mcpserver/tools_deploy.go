package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"asterion-core/internal/plugins"
)

// toolDeployPreview hoy solo sabe "local": compila (si hace falta) y
// arranca el plugin en esta misma máquina, con el mismo 'plugins.Build' +
// 'plugins.Start' que usan 'plugin build'/'plugin start'. Un target que no
// sea "local" se rechaza explícito en vez de fingir un despliegue a una
// nube que no está cableado — mismo criterio que los adapters aws/azure
// de este repo: "publicar una llamada real sin probarla sería peor que no
// tenerla" (ver su comentario de paquete).
func toolDeployPreview() Tool {
	return Tool{
		Name:        "deploy_preview",
		Description: "Compila y arranca un plugin para probarlo. Hoy solo soporta target=\"local\" (esta máquina) — otros targets se rechazan en vez de fingir un despliegue que no existe todavía.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]any{
				"name":   map[string]any{"type": "string", "description": "Nombre del plugin instalado."},
				"target": map[string]any{"type": "string", "enum": []string{"local"}, "description": "Default: \"local\"."},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) Result {
			name := argString(args, "name", "")
			if name == "" {
				return fail("falta 'name'")
			}
			target := argString(args, "target", "local")
			if target != "local" {
				return fail(fmt.Sprintf("target %q no soportado todavía — por ahora deploy_preview solo sabe \"local\" (esta máquina)", target))
			}

			if log, err := plugins.Build(name); err != nil {
				return fail(fmt.Sprintf("no pude compilar %q:\n%s\n%v", name, log, err))
			}

			installed, err := plugins.Start(name)
			if err != nil {
				if strings.Contains(err.Error(), "ya está corriendo") {
					installed, statusErr := plugins.Status(name)
					if statusErr == nil {
						return ok(fmt.Sprintf("%q ya estaba corriendo: http://127.0.0.1:%d", name, installed.Port))
					}
				}
				return fail(err.Error())
			}
			return ok(fmt.Sprintf("%q arrancó: http://127.0.0.1:%d%s", name, installed.Port, installed.Manifest.HealthPath))
		},
	}
}
