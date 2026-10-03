package mcpserver

// AllTools arma las 7 herramientas que expone 'asterion mcp serve'. El
// orden es el que un cliente MCP va a mostrar en su lista — de "solo
// mirar" a "puede llegar a crear algo": inspect_project/run_service nunca
// tocan nada; create_environment es la única que puede levantar un
// contenedor, y solo porque LLAMARLA es, en sí, el pedido explícito que
// el resto de este proyecto exige antes de crear infraestructura.
func AllTools() []Tool {
	byName := map[string]Tool{
		"inspect_project":    toolInspectProject(),
		"run_service":        toolRunService(),
		"create_environment": toolCreateEnvironment(),
		"read_logs":          toolReadLogs(),
		"run_tests":          toolRunTests(),
		"deploy_preview":     toolDeployPreview(),
	}
	byName["request_capability"] = toolRequestCapability(byName)

	order := []string{
		"inspect_project", "run_service", "create_environment",
		"request_capability", "read_logs", "run_tests", "deploy_preview",
	}
	out := make([]Tool, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out
}
