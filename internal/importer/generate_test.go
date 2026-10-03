package importer

import (
	"fmt"
	"strings"
	"testing"

	langparser "github.com/Tarafagat/asterion-language/parser"
	"github.com/Tarafagat/asterion-language/pluginmanifest"
	"github.com/Tarafagat/asterion-plugin-contract/apc"
)

// compileAsterionText corre el mismo compilador que usa
// 'asterion plugin from-asterion' — la prueba real de que un app.asterion
// generado no es solo texto que parece correcto, sino que de verdad
// compila y pasa la validación del contrato.
func compileAsterionText(t *testing.T, text string) (apc.Manifest, error) {
	t.Helper()
	prog, parseDiags := langparser.Parse([]byte(text), "generado.asterion")
	if parseDiags.HasErrors() {
		return apc.Manifest{}, fmt.Errorf("parse: %s", parseDiags.String())
	}
	manifest, compileDiags := pluginmanifest.Compile(prog)
	if compileDiags.HasErrors() {
		return apc.Manifest{}, fmt.Errorf("compile: %s", compileDiags.String())
	}
	return manifest, nil
}

// El caso real que rompió la primera versión de esta función: un servicio
// detectado por patrones de env (DATABASE_URL) excluía su clave de
// Contract.config en vez de dejarla ahí con type="secret" — y
// apc.Manifest.Validate() exige que todo maps_* exista en config_schema.
// Un app.asterion generado que no compila es el peor resultado posible de
// 'asterion import': parece terminado y no lo está.
func TestGenerateProduceUnArchivoQueCompila(t *testing.T) {
	s := &Scan{
		Dir:      ".",
		Name:     Finding{Value: "acme", Confidence: Declared, Source: "package.json"},
		Version:  Finding{Value: "0.1.0", Confidence: Guessed, Source: "no detectado"},
		Language: Finding{Value: "node", Confidence: Declared, Source: "package.json"},
		Start:    Finding{Value: "node dist/server.js", Confidence: Declared, Source: "Dockerfile"},
		Port:     Finding{Value: "4000", Confidence: Declared, Source: "Dockerfile"},
		Configs: []ConfigKey{
			{Key: "DATABASE_URL", Type: "secret", Required: true, Source: ".env.example"},
			{Key: "APP_NAME", Type: "string", Required: false, Default: "acme", Source: ".env.example"},
		},
		Services: []ServiceCandidate{
			{Name: "db", Kind: "postgres", Version: "16", MapsURL: "DATABASE_URL", Source: "docker-compose.yml"},
		},
	}

	text := Generate(s)
	m, err := compileAsterionText(t, text)
	if err != nil {
		t.Fatalf("el .asterion generado no compiló: %v\n--- contenido ---\n%s", err, text)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("el manifiesto generado no pasa Validate(): %v\n--- contenido ---\n%s", err, text)
	}
}

// Un servicio sin ningún maps_host/maps_url detectado tiene que generar un
// Contract.service IGUAL (sin inventar una clave), con el ⚠ explícito —
// nunca quedarse sin emitir el servicio solo porque falta ese dato.
func TestGenerateServicioSinMapeoQuedaMarcado(t *testing.T) {
	s := &Scan{
		Name:     Finding{Value: "acme", Confidence: Guessed, Source: "carpeta"},
		Version:  Finding{Value: "0.1.0", Confidence: Guessed, Source: "no detectado"},
		Services: []ServiceCandidate{{Name: "db", Kind: "postgres", Source: "docker-compose.yml"}},
	}
	text := Generate(s)
	if !strings.Contains(text, `Contract.service(`) {
		t.Fatal("tiene que emitir el Contract.service igual, aunque no tenga ningún maps_*")
	}
	if !strings.Contains(text, "⚠") {
		t.Fatal("un servicio sin maps_host/maps_url tiene que quedar marcado, no silencioso")
	}
}

// Dos corridas de Generate sobre el mismo Scan tienen que dar el mismo
// texto — el orden de un map en Go no es estable, y esto ya rompió una
// vez (ver el comentario sobre orden fijo en detect_services.go).
func TestGenerateEsDeterministico(t *testing.T) {
	s := &Scan{
		Name:    Finding{Value: "acme", Confidence: Declared, Source: "package.json"},
		Version: Finding{Value: "0.1.0", Confidence: Guessed, Source: "x"},
		Services: []ServiceCandidate{
			{Name: "db", Kind: "postgres", MapsHost: "DB_HOST", MapsPort: "DB_PORT", MapsUser: "DB_USER",
				MapsPassword: "DB_PASSWORD", MapsDatabase: "DB_NAME", Source: "x"},
		},
	}
	first := Generate(s)
	for i := 0; i < 20; i++ {
		if Generate(s) != first {
			t.Fatal("Generate() dio un resultado distinto en una corrida repetida sobre el mismo Scan")
		}
	}
}
