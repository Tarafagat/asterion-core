package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// callLine manda una request JSON-RPC de una línea al Server y devuelve su
// response ya decodificada — el helper que todos los tests de este
// archivo reusan para no repetir el armado de bytes.Buffer en cada caso.
func callLine(t *testing.T, srv *Server, line string) map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(line+"\n"), &out); err != nil {
		t.Fatalf("Serve() falló: %v", err)
	}
	var resp map[string]any
	if out.Len() == 0 {
		return nil // una notification no lleva response
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("la response no es JSON válido: %v\nsalida: %s", err, out.String())
	}
	return resp
}

func testTool(name string, text string, isErr bool) Tool {
	return Tool{
		Name: name, Description: "d",
		InputSchema: map[string]any{"type": "object"},
		Handler:     func(ctx context.Context, args map[string]any) Result { return Result{Text: text, IsError: isErr} },
	}
}

func TestInitializeDevuelveLaMismaVersionQuePidioElCliente(t *testing.T) {
	srv := New("v1.0", nil)
	resp := callLine(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01"}}`)
	result := resp["result"].(map[string]any)
	if result["protocolVersion"] != "2099-01-01" {
		t.Errorf("protocolVersion = %v, esperaba que devolviera la misma que pidió el cliente", result["protocolVersion"])
	}
}

func TestInitializeSinVersionUsaLaPropia(t *testing.T) {
	srv := New("v1.0", nil)
	resp := callLine(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	result := resp["result"].(map[string]any)
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, esperaba el default %q", result["protocolVersion"], protocolVersion)
	}
}

func TestToolsListDevuelveLasToolsRegistradas(t *testing.T) {
	srv := New("v1.0", []Tool{testTool("a", "", false), testTool("b", "", false)})
	resp := callLine(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := resp["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("esperaba 2 tools, hubo %d", len(tools))
	}
	first := tools[0].(map[string]any)
	if first["name"] != "a" {
		t.Errorf("tools[0].name = %v, esperaba \"a\"", first["name"])
	}
}

func TestToolsCallDespachaALaToolCorrectaYPropagaIsError(t *testing.T) {
	srv := New("v1.0", []Tool{testTool("falla", "algo salió mal", true), testTool("ok", "todo bien", false)})

	resp := callLine(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"falla","arguments":{}}}`)
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Error("isError tiene que ser true cuando el handler devuelve fail()")
	}
	content := result["content"].([]any)[0].(map[string]any)
	if content["text"] != "algo salió mal" {
		t.Errorf("text = %v, esperaba el texto del handler", content["text"])
	}

	resp = callLine(t, srv, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ok","arguments":{}}}`)
	result = resp["result"].(map[string]any)
	if result["isError"] != false {
		t.Error("isError tiene que ser false cuando el handler devuelve ok()")
	}
}

func TestToolsCallSobreUnaToolInexistenteDevuelveError(t *testing.T) {
	srv := New("v1.0", nil)
	resp := callLine(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"no_existe"}}`)
	if resp["error"] == nil {
		t.Fatal("esperaba un error JSON-RPC, no un result")
	}
}

func TestMetodoDesconocidoDevuelveError(t *testing.T) {
	srv := New("v1.0", nil)
	resp := callLine(t, srv, `{"jsonrpc":"2.0","id":1,"method":"algo/que/no/existe"}`)
	if resp["error"] == nil {
		t.Fatal("esperaba un error JSON-RPC")
	}
}

// Una notification (sin "id") no lleva response — ni siquiera un error, ni
// un objeto vacío: nada escrito a w. Un cliente MCP real manda
// 'notifications/initialized' así, y escribir cualquier cosa ahí
// rompería el framing de "un mensaje por línea" del lado del cliente.
func TestNotificationNoEscribeNingunaResponse(t *testing.T) {
	srv := New("v1.0", nil)
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"), &out); err != nil {
		t.Fatalf("Serve() falló: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("una notification no debería producir ninguna salida, produjo: %q", out.String())
	}
}

// Una línea que no es JSON válido no puede tirar abajo el servidor — un
// cliente real nunca manda esto, pero si algo lo hiciera, el resto de la
// sesión tiene que seguir funcionando.
func TestLineaInvalidaNoRompeElServidor(t *testing.T) {
	srv := New("v1.0", []Tool{testTool("ok", "bien", false)})
	var out bytes.Buffer
	input := "esto no es json\n" + `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ok"}}` + "\n"
	if err := srv.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve() falló: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("la request válida después de la inválida no generó response: %v", err)
	}
}
