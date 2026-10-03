package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"asterion-core/internal/mcpserver"
)

// mcpCmd es la pieza que deja que CUALQUIER agente de código que ya exista
// (Claude Code, Cursor, Copilot, Gemini CLI...) le pida infraestructura
// real a Asterion sin saber hablar con Docker, psql, un SDK de nube ni
// nada de eso — el objetivo explícito es no competir con esos agentes,
// sino que todos ellos quieran usar esto.
func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Servidor MCP de Asterion — expone infraestructura real a cualquier agente de código",
	}
	cmd.AddCommand(mcpServeCmd(), mcpInitCmd())
	return cmd
}

func mcpServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Corre el servidor MCP sobre stdio (esto es lo que un cliente MCP ejecuta, no algo para correr a mano)",
		Long: "JSON-RPC 2.0 sobre stdin/stdout, un mensaje por línea — el transporte estándar de MCP\n" +
			"para servidores locales. Expone 7 herramientas (inspect_project, run_service,\n" +
			"create_environment, request_capability, read_logs, run_tests, deploy_preview);\n" +
			"ver 'asterion mcp init' para dejarlo declarado en este proyecto y que un agente lo\n" +
			"encuentre solo.\n\n" +
			"Se queda corriendo hasta que el cliente cierra su stdin (Ctrl+C también funciona,\n" +
			"para probarlo a mano) — no es un comando pensado para usarse interactivamente.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			srv := mcpserver.New(version, mcpserver.AllTools())
			return srv.Serve(ctx, os.Stdin, os.Stdout)
		},
	}
}

func mcpInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Declara el servidor MCP de Asterion en este proyecto (.mcp.json) y escribe ASTERION.md",
		Long: "Escribe (o agrega una entrada a, si ya existe) .mcp.json con un servidor \"asterion\"\n" +
			"que corre 'asterion mcp serve' — el mismo archivo y formato que ya usa Claude Code\n" +
			"para declarar servidores MCP de un proyecto, así que un agente que ya sepa leer\n" +
			".mcp.json lo encuentra sin ningún paso extra.\n\n" +
			"También escribe ASTERION.md: no lo lee ningún agente solo (no es un CLAUDE.md), es\n" +
			"la referencia de qué puede pedir y cómo — pensado para que un CLAUDE.md (u otro\n" +
			"archivo de instrucciones del agente que corresponda) lo mencione.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPInit(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Sobrescribir ASTERION.md si ya existe (.mcp.json nunca se sobrescribe entero, se le agrega la entrada)")
	return cmd
}

const mcpConfigFile = ".mcp.json"
const asterionMDFile = "ASTERION.md"

func runMCPInit(force bool) error {
	if err := ensureMCPServerEntry(); err != nil {
		return err
	}
	if err := writeAsterionMD(force); err != nil {
		return err
	}
	appendClaudeMDPointer()

	fmt.Printf("✓ %s: servidor \"asterion\" declarado\n", mcpConfigFile)
	fmt.Printf("✓ %s escrito\n", asterionMDFile)
	fmt.Println("\nUn agente que lea .mcp.json (Claude Code, y otros que adopten el mismo formato)")
	fmt.Println("ya puede llamar a sus herramientas. Para probarlo a mano: 'asterion mcp serve'")
	fmt.Println("habla JSON-RPC por stdin/stdout — no es para teclear directo, pero confirma que")
	fmt.Println("arranca sin errores.")
	return nil
}

func ensureMCPServerEntry() error {
	doc := map[string]any{}
	if data, err := os.ReadFile(mcpConfigFile); err == nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s ya existe pero no es JSON válido — arréglalo a mano antes: %w", mcpConfigFile, err)
		}
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if _, exists := servers["asterion"]; exists {
		return nil // ya estaba — no lo piso, podría tener flags/env agregados a mano
	}
	servers["asterion"] = map[string]any{
		"command": "asterion",
		"args":    []string{"mcp", "serve"},
	}
	doc["mcpServers"] = servers

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(mcpConfigFile, append(out, '\n'), 0o644)
}

func writeAsterionMD(force bool) error {
	if !force {
		if _, err := os.Stat(asterionMDFile); err == nil {
			return fmt.Errorf("%s ya existe — usa --force para sobrescribirlo", asterionMDFile)
		}
	}
	return os.WriteFile(asterionMDFile, []byte(asterionMDTemplate), 0o644)
}

// appendClaudeMDPointer suma una línea a un CLAUDE.md que ya exista en
// este directorio — nunca lo crea (eso lo hace 'claude /init', no este
// comando) y nunca duplica la línea si ya está. Mejor esfuerzo: si falla
// por cualquier motivo, 'mcp init' igual ya dejó lo que importa (.mcp.json
// + ASTERION.md) y no corta por esto.
func appendClaudeMDPointer() {
	const pointer = "\nPara infraestructura real (bases de datos, Redis, logs, despliegues de prueba) " +
		"este proyecto expone un servidor MCP propio — ver [ASTERION.md](ASTERION.md).\n"
	data, err := os.ReadFile("CLAUDE.md")
	if err != nil {
		return // no hay CLAUDE.md acá — no es este comando el que lo crea
	}
	if strings.Contains(string(data), "ASTERION.md") {
		return
	}
	f, err := os.OpenFile("CLAUDE.md", os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(pointer)
	fmt.Println("✓ CLAUDE.md: agregada una referencia a ASTERION.md")
}

const asterionMDTemplate = `# Asterion — infraestructura para agentes de código

Este proyecto tiene un servidor MCP propio (` + "`asterion mcp serve`" + `, declarado en
` + "`.mcp.json`" + `) que deja pedir infraestructura real sin tocarla a mano ni tener
acceso de root a esta máquina. La idea: en vez de que el agente
corra ` + "`docker run postgres`" + ` a ciegas, le pides a Asterion un Postgres y
te da uno ya configurado, con credenciales de aplicación — nunca las de
administrador del motor, nunca un shell del host.

## Herramientas disponibles

- **inspect_project(path?)** — de solo lectura. Si el directorio ya es un
  plugin de Asterion (tiene ` + "`plugin.yaml`" + `), devuelve lo que declara y, si
  está instalado, su diagnóstico de salud. Si no, detecta lenguaje,
  comando de arranque, variables de entorno y servicios externos como lo
  haría ` + "`asterion import`" + `. Nunca escribe nada.

- **run_service(kind, name?)** — ¿ya hay un postgres/mysql/mariadb/redis
  alcanzable ahora mismo? Solo detecta, nunca crea nada.

- **create_environment(kind, name?)** — asegura un motor USABLE: reusa uno
  que ya esté corriendo, o si no hay ninguno, levanta uno en un
  contenedor propio. Devuelve host/puerto/usuario/contraseña de un
  usuario de aplicación recién creado — nunca de administrador.
  Llamar a esta tool ES el pedido explícito que autoriza crear el
  contenedor; no hace falta (ni existe) ningún flag extra.

- **request_capability(capability, mode?, name?, target?)** — un único
  punto de entrada a las de arriba por nombre de capability
  (` + "`database`" + `, ` + "`cache`" + `, ` + "`logs`" + `, ` + "`tests`" + `, ` + "`deploy`" + `) para quien prefiere no
  acordarse de 7 nombres de tool. Dispara exactamente la misma función
  que la tool dedicada — no hay una capa de permisos aparte.

- **read_logs(name, lines?)** — las últimas líneas del log de un plugin
  instalado.

- **run_tests(name)** — corre el test runner convencional del lenguaje
  declarado (` + "`go test ./...`" + `, ` + "`pytest`" + `) dentro de la carpeta del plugin.
  Nunca un comando arbitrario que el agente pase como texto.

- **deploy_preview(name, target?)** — compila y arranca un plugin para
  probarlo. Hoy solo ` + "`target=\"local\"`" + ` (esta máquina); otros destinos se
  van a sumar, y mientras tanto se rechazan en vez de fingir un
  despliegue que no existe.

## Cómo llegar a Asterion sin pasar por acá

Todo lo de arriba también es un comando de ` + "`asterion`" + ` directo, por si
prefieres ejecutarlo directamente en vez de que lo llame el agente:

- ` + "`asterion doctor [plugin]`" + ` — diagnóstico de salud/entorno/seguridad/deployment.
- ` + "`asterion import [dir]`" + ` — genera un ` + "`app.asterion`" + ` de un proyecto existente.
- ` + "`asterion plugin services [up|connect] <plugin>`" + ` — detecta/configura/conecta
  los servicios externos de un plugin ya instalado.
`
