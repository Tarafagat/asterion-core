package mcpserver

import "context"

// Tool es una herramienta expuesta por 'asterion mcp serve'. InputSchema
// es JSON Schema crudo (lo que 'tools/list' necesita para que el cliente
// MCP sepa qué argumentos ofrecer) — un map[string]any en vez de un tipo
// propio porque es exactamente lo que json.Marshal necesita, sin una capa
// de indirección que no aporta nada acá.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(ctx context.Context, args map[string]any) Result
}

// Result es lo que una Tool devuelve. Texto plano, no JSON estructurado:
// es lo que un modelo de lenguaje consume mejor, y es lo que el resto de
// este CLI ya imprime en consola — reusar ese mismo texto en vez de
// inventar un segundo formato de salida para cada comando.
type Result struct {
	Text    string
	IsError bool
}

func ok(text string) Result  { return Result{Text: text} }
func fail(text string) Result { return Result{Text: text, IsError: true} }

// argString/argBool/argInt leen un argumento del map con un default — las
// tools de este paquete los usan todos, y así cada handler no repite el
// mismo type assertion defensivo.
func argString(args map[string]any, key, def string) string {
	if v, ok := args[key].(string); ok && v != "" {
		return v
	}
	return def
}

func argInt(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64: // json.Unmarshal de un número a 'any' siempre da float64
		return int(v)
	case int:
		return v
	}
	return def
}
