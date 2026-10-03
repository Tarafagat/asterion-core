package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// mcpConfigPaths son los lugares donde los agentes de código que existen
// hoy guardan su config de servidores MCP, relativos al directorio desde
// donde se corre 'asterion doctor'. No hay un estándar único todavía —
// Claude Code usa .mcp.json en la raíz del proyecto, Cursor y VS Code
// Copilot replican el mismo formato bajo su propia carpeta.
var mcpConfigPaths = []string{".mcp.json", ".cursor/mcp.json", ".vscode/mcp.json"}

// checkAI detecta si hay agentes de código y MCP alrededor de este
// proyecto — no para competir con ellos (ver el comentario de paquete de
// cmd/asterion/mcp.go), sino para que 'doctor' le diga a quien lo corre
// qué tiene disponible para integrarse.
func checkAI(ctx context.Context) []Check {
	var out []Check

	if _, err := exec.LookPath("claude"); err == nil {
		out = append(out, Check{Section: "AI", Name: "Claude Code", Severity: OK, Detail: "binario 'claude' encontrado en el PATH"})
	} else if _, err := os.Stat(".claude"); err == nil {
		out = append(out, Check{Section: "AI", Name: "Claude Code", Severity: OK, Detail: "carpeta .claude/ presente en este directorio"})
	} else {
		out = append(out, Check{Section: "AI", Name: "Claude Code", Severity: NA, Detail: "no detectado"})
	}

	total := 0
	var withServers []string
	for _, p := range mcpConfigPaths {
		n, err := countMCPServers(p)
		if err != nil {
			continue
		}
		total += n
		if n > 0 {
			withServers = append(withServers, fmt.Sprintf("%s (%d)", p, n))
		}
	}
	if total == 0 {
		out = append(out, Check{Section: "AI", Name: "MCP", Severity: NA, Detail: "ningún .mcp.json encontrado en este directorio"})
	} else {
		detail := fmt.Sprintf("%d servidor(es) declarados", total)
		for _, w := range withServers {
			detail += " — " + w
		}
		out = append(out, Check{Section: "AI", Name: "MCP", Severity: OK, Detail: detail})
	}

	out = append(out, checkAGCA(ctx)...)
	return out
}

func countMCPServers(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return 0, err
	}
	return len(doc.MCPServers), nil
}

// checkAGCA no asume un interruptor global: AGCA se declara por archivo
// (AGCA.*/Tool.* en un .asterion), así que "habilitado" significa
// concretamente "hay al menos un .asterion en este directorio que lo usa"
// — un grep de texto, no una compilación completa: un reporte de salud no
// debe fallar porque ese archivo tenga, aparte, un error de sintaxis en
// otra parte.
func checkAGCA(ctx context.Context) []Check {
	matches, err := filepath.Glob("*.asterion")
	if err != nil {
		return []Check{{Section: "AI", Name: "AGCA", Severity: NA, Detail: "no pude buscar archivos .asterion"}}
	}
	more, _ := filepath.Glob("*/*.asterion")
	matches = append(matches, more...)

	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		text := string(data)
		if strings.Contains(text, "AGCA.") || strings.Contains(text, "Tool.define") || strings.Contains(text, "Tool.capability") {
			return []Check{{Section: "AI", Name: "AGCA", Severity: OK, Detail: "detectado en " + m}}
		}
	}
	return []Check{{Section: "AI", Name: "AGCA", Severity: NA, Detail: "disabled — ningún .asterion con AGCA.*/Tool.* en este directorio"}}
}
