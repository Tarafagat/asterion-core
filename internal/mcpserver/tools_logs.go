package mcpserver

import (
	"context"
	"fmt"
	"os"
	"strings"

	"asterion-core/internal/plugins"
)

// toolReadLogs reusa el mismo archivo que 'asterion plugin logs' (lo que
// Start() redirige con stdout+stderr), con una diferencia a propósito: no
// hay '--follow' acá — una llamada MCP es pedido/respuesta, no un stream;
// un agente que quiera ver logs en vivo tiene que volver a llamar a la
// tool, no quedarse esperando una conexión abierta.
func toolReadLogs() Tool {
	return Tool{
		Name:        "read_logs",
		Description: "Devuelve las últimas líneas del log (stdout+stderr) de un plugin instalado.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]any{
				"name":  map[string]any{"type": "string", "description": "Nombre del plugin instalado."},
				"lines": map[string]any{"type": "integer", "description": "Cuántas líneas finales (default 50)."},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) Result {
			name := argString(args, "name", "")
			if name == "" {
				return fail("falta 'name'")
			}
			n := argInt(args, "lines", 50)

			installed, err := plugins.Get(name)
			if err != nil {
				return fail(err.Error())
			}
			path, err := plugins.LogPath(installed)
			if err != nil {
				return fail(err.Error())
			}
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return ok(fmt.Sprintf("%q todavía no generó ningún log en %s", name, path))
				}
				return fail(err.Error())
			}
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			start := 0
			if len(lines) > n {
				start = len(lines) - n
			}
			return ok(strings.Join(lines[start:], "\n"))
		},
	}
}
