// Package mcpserver implementa el lado servidor del Model Context Protocol
// sobre stdio — lo que 'asterion mcp serve' expone. El objetivo no es
// competir con los agentes de código que ya existen (Claude Code, Cursor,
// Copilot, Gemini CLI...): es que cualquiera de ellos pueda pedirle a
// Asterion infraestructura real ("necesito un Postgres") sin que el
// agente mismo necesite acceso de root al host ni saber hablar con
// Docker, psql o un SDK de nube — eso ya lo sabe hacer Asterion, y lo hace
// con las mismas reglas que el resto de este proyecto: detectar primero,
// configurar lo que ya existe, y crear un contenedor solo cuando se pide
// explícitamente (acá, "explícitamente" es que el agente llame la
// herramienta por su nombre — no hay otra forma de llegar a ella).
//
// El protocolo en sí es JSON-RPC 2.0 sobre stdin/stdout, un mensaje por
// línea (newline-delimited JSON) — el transporte estándar de MCP para
// servidores locales. No hace falta un SDK de MCP para esto: son structs
// de encoding/json y un loop de lectura, mismo criterio de cero
// dependencias nuevas que ya rige todo asterion-core.
package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
)

// protocolVersion es la que este servidor sabe hablar. 'initialize' le
// devuelve al cliente la MISMA versión que pidió en vez de forzar una
// propia — el envelope JSON-RPC y la forma de tools/list y tools/call no
// cambiaron entre las versiones publicadas del protocolo, así que
// negociar por eco es seguro y evita que un cliente con una versión
// apenas distinta rechace la conexión por un string que no coincide.
const protocolVersion = "2024-11-05"

const serverName = "asterion-mcp"

// Server sirve 'tools/list' y 'tools/call' leyendo requests de r y
// escribiendo responses a w — normalmente os.Stdin/os.Stdout, separado acá
// para poder probarlo con un buffer en los tests.
type Server struct {
	tools   []Tool
	version string
}

func New(version string, tools []Tool) *Server {
	return &Server{tools: tools, version: version}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve corre el loop principal: lee una request JSON-RPC por línea de r,
// la despacha, y si trae 'id' (no es una notification) escribe la
// response correspondiente a w. Termina cuando r se cierra (EOF) — el
// cliente MCP cierra el stdin del proceso para pedirle que salga, no hay
// un mensaje explícito de "shutdown" que haga falta manejar aparte.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // un tools/call con un log largo de vuelta puede pesar

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// No se puede ni saber el id — esto no debería pasar nunca
			// hablando con un cliente MCP real, se loguea a stderr (nunca
			// a stdout: ahí solo van mensajes del protocolo) y se sigue.
			log.Printf("asterion-mcp: línea no es JSON-RPC válido: %v", err)
			continue
		}

		result, rpcErr := s.dispatch(ctx, req)
		if req.ID == nil {
			continue // es una notification (ej. 'notifications/initialized') — no lleva response
		}
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
		out, err := json.Marshal(resp)
		if err != nil {
			log.Printf("asterion-mcp: no pude serializar la response: %v", err)
			continue
		}
		if _, err := w.Write(append(out, '\n')); err != nil {
			return fmt.Errorf("no pude escribir a stdout: %w", err)
		}
	}
	return scanner.Err()
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.Params), nil
	case "notifications/initialized":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return s.handleToolsList(), nil
	case "tools/call":
		return s.handleToolsCall(ctx, req.Params)
	default:
		return nil, &rpcError{Code: -32601, Message: "método no soportado: " + req.Method}
	}
}

func (s *Server) handleInitialize(params json.RawMessage) any {
	var in struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &in)
	version := in.ProtocolVersion
	if version == "" {
		version = protocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": s.version,
		},
	}
}

func (s *Server) handleToolsList() any {
	out := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return map[string]any{"tools": out}
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var in struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, &rpcError{Code: -32602, Message: "params inválidos: " + err.Error()}
	}

	for _, t := range s.tools {
		if t.Name != in.Name {
			continue
		}
		res := t.Handler(ctx, in.Arguments)
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": res.Text}},
			"isError": res.IsError,
		}, nil
	}
	return nil, &rpcError{Code: -32602, Message: "no existe la herramienta " + in.Name}
}
