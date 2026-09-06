package gcp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"asterion-core/internal/adapters"
)

// withMockComputeServer apunta computeAPIBaseURL (Compute Engine) a un
// httptest.Server durante el test y lo restaura al terminar — mismo
// criterio que projectsURL en internal/adapters/vercel.
func withMockComputeServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	original := computeAPIBaseURL
	computeAPIBaseURL = srv.URL
	t.Cleanup(func() { computeAPIBaseURL = original })
	return srv
}

// fastPolling baja zoneOperationPollInterval a algo instantáneo durante el
// test — sin esto, cada test de CreateInstance tardaría de verdad los 2s
// reales entre sondeos.
func fastPolling(t *testing.T) {
	t.Helper()
	original := zoneOperationPollInterval
	zoneOperationPollInterval = time.Microsecond
	t.Cleanup(func() { zoneOperationPollInterval = original })
}

// testServiceAccountJSON genera un par de claves RSA reales al vuelo y arma
// un JSON de service account con token_uri apuntando a tokenURL (un
// httptest.Server que hace de token endpoint falso de Google) — así
// accessToken() puede firmar y "canjear" un JWT de verdad sin hablar con
// Google real.
func testServiceAccountJSON(t *testing.T, tokenURL string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("no pude generar la clave RSA de prueba: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("no pude serializar la clave: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	sa := map[string]string{
		"project_id":     "test-project",
		"private_key_id": "test-key-id",
		"private_key":    string(pemBytes),
		"client_email":   "test@test-project.iam.gserviceaccount.com",
		"token_uri":      tokenURL,
	}
	raw, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("no pude armar el JSON de la service account: %v", err)
	}
	return string(raw)
}

func fakeTokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "fake-access-token"})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCreateInstance_Success(t *testing.T) {
	fastPolling(t)
	tokenSrv := fakeTokenServer(t)
	saJSON := testServiceAccountJSON(t, tokenSrv.URL)

	var gotInsertBody map[string]any
	var gotAuth string
	pollCount := 0

	withMockComputeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/instances"):
			_ = json.NewDecoder(r.Body).Decode(&gotInsertBody)
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "operation-123"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/operations/"):
			pollCount++
			status := "PENDING"
			if pollCount >= 2 {
				status = "DONE"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/instances/"):
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "web-1", "status": "RUNNING"})
		default:
			t.Fatalf("request inesperado: %s %s", r.Method, r.URL.Path)
		}
	})

	a := New()
	result, err := a.CreateInstance(context.Background(), adapters.InstanceSpec{
		Name:           "web-1",
		Region:         "us-central1-a",
		ShapeCode:      "e2-micro",
		ImageID:        "projects/debian-cloud/global/images/family/debian-12",
		AssignPublicIP: false,
		Credentials:    map[string]string{"service_account_json": saJSON},
	})
	if err != nil {
		t.Fatalf("CreateInstance devolvió error: %v", err)
	}
	if result.ExternalID != "web-1" || result.Status != "running" {
		t.Fatalf("resultado inesperado: %+v", result)
	}
	if gotAuth != "Bearer fake-access-token" {
		t.Fatalf("Authorization inesperado: %q", gotAuth)
	}
	if pollCount < 2 {
		t.Fatalf("esperaba al menos 2 sondeos de la operación (PENDING luego DONE), hubo %d", pollCount)
	}

	machineType, _ := gotInsertBody["machineType"].(string)
	if machineType != "zones/us-central1-a/machineTypes/e2-micro" {
		t.Fatalf("machineType inesperado: %q", machineType)
	}
	disks, _ := gotInsertBody["disks"].([]any)
	if len(disks) != 1 {
		t.Fatalf("esperaba 1 disco, hubo %d", len(disks))
	}
	disk := disks[0].(map[string]any)
	if disk["boot"] != true || disk["autoDelete"] != true {
		t.Fatalf("disco inesperado: %+v", disk)
	}
	initParams := disk["initializeParams"].(map[string]any)
	if initParams["sourceImage"] != "projects/debian-cloud/global/images/family/debian-12" {
		t.Fatalf("sourceImage inesperado: %+v", initParams)
	}
	ifaces, _ := gotInsertBody["networkInterfaces"].([]any)
	if len(ifaces) != 1 {
		t.Fatalf("esperaba 1 networkInterface, hubo %d", len(ifaces))
	}
	iface := ifaces[0].(map[string]any)
	if iface["network"] != "global/networks/default" {
		t.Fatalf("network inesperado: %+v", iface)
	}
	if _, hasAccessConfigs := iface["accessConfigs"]; hasAccessConfigs {
		t.Fatalf("no debería haber accessConfigs con AssignPublicIP=false: %+v", iface)
	}
}

func TestCreateInstance_AssignPublicIP(t *testing.T) {
	fastPolling(t)
	tokenSrv := fakeTokenServer(t)
	saJSON := testServiceAccountJSON(t, tokenSrv.URL)

	var gotInsertBody map[string]any
	withMockComputeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&gotInsertBody)
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "op-1"})
		case strings.Contains(r.URL.Path, "/operations/"):
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "DONE"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "web-2", "status": "PROVISIONING"})
		}
	})

	a := New()
	result, err := a.CreateInstance(context.Background(), adapters.InstanceSpec{
		Name: "web-2", Region: "us-central1-a", ShapeCode: "e2-micro",
		ImageID:        "projects/debian-cloud/global/images/family/debian-12",
		AssignPublicIP: true,
		Credentials:    map[string]string{"service_account_json": saJSON},
	})
	if err != nil {
		t.Fatalf("CreateInstance devolvió error: %v", err)
	}
	if result.Status != "provisioning" {
		t.Fatalf("status inesperado: %q", result.Status)
	}
	ifaces, _ := gotInsertBody["networkInterfaces"].([]any)
	iface := ifaces[0].(map[string]any)
	accessConfigs, ok := iface["accessConfigs"].([]any)
	if !ok || len(accessConfigs) != 1 {
		t.Fatalf("esperaba accessConfigs con AssignPublicIP=true: %+v", iface)
	}
}

func TestCreateInstance_OperationFails(t *testing.T) {
	fastPolling(t)
	tokenSrv := fakeTokenServer(t)
	saJSON := testServiceAccountJSON(t, tokenSrv.URL)

	withMockComputeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "op-fail"})
		case strings.Contains(r.URL.Path, "/operations/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "DONE",
				"error": map[string]any{
					"errors": []map[string]string{
						{"code": "QUOTA_EXCEEDED", "message": "Quota 'CPUS' exceeded"},
					},
				},
			})
		}
	})

	a := New()
	_, err := a.CreateInstance(context.Background(), adapters.InstanceSpec{
		Name: "web-3", Region: "us-central1-a", ShapeCode: "e2-micro",
		ImageID:     "projects/debian-cloud/global/images/family/debian-12",
		Credentials: map[string]string{"service_account_json": saJSON},
	})
	if err == nil {
		t.Fatal("esperaba un error por la operación fallida, no hubo ninguno")
	}
	if !strings.Contains(err.Error(), "QUOTA_EXCEEDED") {
		t.Fatalf("el error debería incluir el motivo real de GCP, fue: %v", err)
	}
}

func TestCreateInstance_MissingCredentials(t *testing.T) {
	a := New()
	_, err := a.CreateInstance(context.Background(), adapters.InstanceSpec{Name: "x", Region: "us-central1-a"})
	if err == nil || !strings.Contains(err.Error(), "service_account_json") {
		t.Fatalf("esperaba un error claro sobre credenciales faltantes, fue: %v", err)
	}
}

func TestCreateInstance_MissingZone(t *testing.T) {
	tokenSrv := fakeTokenServer(t)
	saJSON := testServiceAccountJSON(t, tokenSrv.URL)
	a := New()
	_, err := a.CreateInstance(context.Background(), adapters.InstanceSpec{
		Name:        "x",
		Credentials: map[string]string{"service_account_json": saJSON},
	})
	if err == nil || !strings.Contains(err.Error(), "zona") {
		t.Fatalf("esperaba un error claro sobre la zona faltante, fue: %v", err)
	}
}

func TestCreateInstance_OperationNeverFinishes(t *testing.T) {
	fastPolling(t)
	// Un par de intentos alcanza para probar el timeout sin hacer el test lento.
	originalMax := zoneOperationMaxAttempts
	zoneOperationMaxAttempts = 2
	t.Cleanup(func() { zoneOperationMaxAttempts = originalMax })

	tokenSrv := fakeTokenServer(t)
	saJSON := testServiceAccountJSON(t, tokenSrv.URL)

	withMockComputeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "op-stuck"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "RUNNING"})
		}
	})

	a := New()
	_, err := a.CreateInstance(context.Background(), adapters.InstanceSpec{
		Name: "web-4", Region: "us-central1-a", ShapeCode: "e2-micro",
		ImageID:     "projects/debian-cloud/global/images/family/debian-12",
		Credentials: map[string]string{"service_account_json": saJSON},
	})
	if err == nil || !strings.Contains(err.Error(), "no terminó") {
		t.Fatalf("esperaba un error de timeout, fue: %v", err)
	}
}
