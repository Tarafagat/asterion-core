package cloudmeta

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Los tests contra httptest.Server simulan la respuesta REAL de cada
// proveedor (confirmada contra su doc oficial, ver el comentario de
// paquete y el plan de la sesión) — Detect() en sí prueba SIEMPRE contra
// las URLs/hostnames fijas de cada proveedor (metadata.google.internal,
// 169.254.169.254), así que estos tests ejercitan las funciones probeXxx
// directamente contra un servidor de prueba, no Detect() completo.

func TestProbeGCP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			t.Errorf("esperaba el header Metadata-Flavor: Google, no vino")
		}
		if r.URL.Path != "/computeMetadata/v1/instance/name" {
			t.Errorf("path inesperado: %s", r.URL.Path)
		}
		w.Write([]byte("mi-instancia-gcp"))
	}))
	defer srv.Close()

	body, ok := doRequest(context.Background(), http.MethodGet, srv.URL+"/computeMetadata/v1/instance/name", map[string]string{"Metadata-Flavor": "Google"})
	if !ok {
		t.Fatal("esperaba ok=true")
	}
	if string(body) != "mi-instancia-gcp" {
		t.Fatalf("esperaba 'mi-instancia-gcp', dio %q", body)
	}
}

func TestProbeAzureJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata") != "true" {
			t.Errorf("esperaba el header Metadata: true, no vino")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"compute":{"name":"mi-vm-azure","vmId":"abc-123"}}`))
	}))
	defer srv.Close()

	body, ok := doRequest(context.Background(), http.MethodGet, srv.URL+"/metadata/instance?api-version=2021-02-01", map[string]string{"Metadata": "true"})
	if !ok {
		t.Fatal("esperaba ok=true")
	}
	var parsed struct {
		Compute struct {
			Name string `json:"name"`
		} `json:"compute"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Compute.Name != "mi-vm-azure" {
		t.Fatalf("esperaba 'mi-vm-azure', dio %q", parsed.Compute.Name)
	}
}

func TestProbeOCIJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer Oracle" {
			t.Errorf("esperaba el header Authorization: Bearer Oracle, no vino")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"displayName":"mi-instancia-oci","id":"ocid1.instance.oc1..."}`))
	}))
	defer srv.Close()

	body, ok := doRequest(context.Background(), http.MethodGet, srv.URL+"/opc/v2/instance/", map[string]string{"Authorization": "Bearer Oracle"})
	if !ok {
		t.Fatal("esperaba ok=true")
	}
	var parsed struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.DisplayName != "mi-instancia-oci" {
		t.Fatalf("esperaba 'mi-instancia-oci', dio %q", parsed.DisplayName)
	}
}

func TestProbeAWSTwoStepToken(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/latest/api/token":
			if r.Header.Get("X-aws-ec2-metadata-token-ttl-seconds") == "" {
				t.Errorf("esperaba el header X-aws-ec2-metadata-token-ttl-seconds")
			}
			w.Write([]byte("fake-token-xyz"))
		case r.Method == http.MethodGet && r.URL.Path == "/latest/meta-data/instance-id":
			gotToken = r.Header.Get("X-aws-ec2-metadata-token")
			w.Write([]byte("i-0123456789abcdef0"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	tokenBody, ok := doRequest(context.Background(), http.MethodPut, srv.URL+"/latest/api/token", map[string]string{"X-aws-ec2-metadata-token-ttl-seconds": "60"})
	if !ok {
		t.Fatal("esperaba ok=true en el paso del token")
	}
	if string(tokenBody) != "fake-token-xyz" {
		t.Fatalf("token inesperado: %q", tokenBody)
	}

	idBody, ok := doRequest(context.Background(), http.MethodGet, srv.URL+"/latest/meta-data/instance-id", map[string]string{"X-aws-ec2-metadata-token": string(tokenBody)})
	if !ok {
		t.Fatal("esperaba ok=true en el paso del instance-id")
	}
	if string(idBody) != "i-0123456789abcdef0" {
		t.Fatalf("instance-id inesperado: %q", idBody)
	}
	if gotToken != "fake-token-xyz" {
		t.Fatalf("el segundo paso no mandó el token del primero, mandó %q", gotToken)
	}
}

// TestDetectOnRealMachine corre Detect() de verdad, sin ningún mock —
// esta Mac no es una VM de ningún proveedor cloud, así que este es
// exactamente el escenario "server privado/bare-metal" que hay que
// garantizar: nunca debe tardar más de unos pocos segundos ni devolver
// un error (Detect no devuelve error en absoluto), y Provider debe quedar
// vacío.
func TestDetectOnRealMachine(t *testing.T) {
	start := time.Now()
	identity := Detect(context.Background())
	elapsed := time.Since(start)

	t.Logf("Detect() tardó %s, devolvió %+v", elapsed, identity)
	if identity.Provider != "" {
		t.Fatalf("esta máquina no es una VM cloud conocida, pero Detect dijo Provider=%q", identity.Provider)
	}
	if elapsed > 6*time.Second {
		t.Fatalf("Detect() tardó demasiado (%s) para un server privado — debería fallar rápido en los 4 intentos", elapsed)
	}
}
