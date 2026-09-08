package oci

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"asterion-core/internal/adapters"
)

// testCreds genera un par RSA real al vuelo y arma unas apiKeyCredentials
// de prueba — mismo criterio que testServiceAccountJSON en
// internal/adapters/gcp/adapter_test.go, adaptado a la forma de
// credenciales de OCI (4 campos sueltos, no un JSON único).
func testCreds(t *testing.T) (apiKeyCredentials, *rsa.PublicKey) {
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
	creds := apiKeyCredentials{
		UserOCID:    "ocid1.user.oc1..testuser",
		TenancyOCID: "ocid1.tenancy.oc1..testtenancy",
		Fingerprint: "aa:bb:cc:dd",
		PrivateKey:  string(pemBytes),
	}
	return creds, &key.PublicKey
}

func withMockOCIServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	originalIAAS, originalIdentity := iaasBaseURL, identityBaseURL
	iaasBaseURL = func(string) string { return srv.URL }
	identityBaseURL = func(string) string { return srv.URL }
	t.Cleanup(func() { iaasBaseURL, identityBaseURL = originalIAAS, originalIdentity })
	return srv
}

func fastPolling(t *testing.T) {
	t.Helper()
	original := instanceOperationPollInterval
	instanceOperationPollInterval = time.Microsecond
	t.Cleanup(func() { instanceOperationPollInterval = original })
}

// keyIDPattern valida la forma exacta que OCI exige para keyId:
// "<tenancyOCID>/<userOCID>/<fingerprint>".
var keyIDPattern = regexp.MustCompile(`keyId="([^"]+)"`)

func TestSignRequest_ProducesVerifiableSignature(t *testing.T) {
	creds, pub := testCreds(t)

	req, err := http.NewRequest(http.MethodGet, "https://iaas.us-ashburn-1.oraclecloud.com/20160918/instances?compartmentId=abc", nil)
	if err != nil {
		t.Fatalf("no pude armar el request de prueba: %v", err)
	}
	if err := signRequest(req, creds, nil); err != nil {
		t.Fatalf("signRequest falló: %v", err)
	}

	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, `Signature version="1"`) {
		t.Fatalf("Authorization no tiene la forma esperada: %s", auth)
	}
	wantKeyID := creds.TenancyOCID + "/" + creds.UserOCID + "/" + creds.Fingerprint
	m := keyIDPattern.FindStringSubmatch(auth)
	if m == nil || m[1] != wantKeyID {
		t.Fatalf("keyId incorrecto en %s (esperaba %s)", auth, wantKeyID)
	}

	// Reconstruye el signing string exactamente como debería haberlo
	// armado signRequest, y confirma que la firma efectivamente verifica
	// contra la clave pública — esto es lo único que prueba que el
	// algoritmo de firma es el que OCI espera de verdad, no solo que "se
	// mandó algo" a un servidor falso.
	dateValue := req.Header.Get("Date")
	signingString := "(request-target): get /20160918/instances?compartmentId=abc\n" +
		"date: " + dateValue + "\n" +
		"host: " + req.URL.Host
	hashed := sha256.Sum256([]byte(signingString))

	sigMatch := regexp.MustCompile(`signature="([^"]+)"`).FindStringSubmatch(auth)
	if sigMatch == nil {
		t.Fatalf("no encontré signature en %s", auth)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sigMatch[1])
	if err != nil {
		t.Fatalf("la firma no es base64 válido: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, hashed[:], sigBytes); err != nil {
		t.Fatalf("la firma no verifica contra la clave pública: %v", err)
	}
}

func TestSignRequest_WithBody_SignsContentHeaders(t *testing.T) {
	creds, _ := testCreds(t)
	body := []byte(`{"displayName":"test"}`)

	req, err := http.NewRequest(http.MethodPost, "https://iaas.us-ashburn-1.oraclecloud.com/20160918/instances", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("no pude armar el request de prueba: %v", err)
	}
	if err := signRequest(req, creds, body); err != nil {
		t.Fatalf("signRequest falló: %v", err)
	}

	auth := req.Header.Get("Authorization")
	for _, want := range []string{"x-content-sha256", "content-type", "content-length"} {
		if !strings.Contains(auth, want) {
			t.Errorf("esperaba %q entre los headers firmados de %s", want, auth)
		}
	}
	if req.Header.Get("X-Content-Sha256") == "" {
		t.Error("X-Content-Sha256 no quedó seteado en el request")
	}
}

func TestListInstances_Success_WithPagination(t *testing.T) {
	creds, _ := testCreds(t)
	calls := 0
	srv := withMockOCIServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("opc-next-page", "page2token")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"ocid1.instance.oc1..aaa","lifecycleState":"RUNNING"}]`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"ocid1.instance.oc1..bbb","lifecycleState":"STOPPED"}]`))
	})
	_ = srv

	a := New()
	results, err := a.ListInstances(context.Background(), instancesDiscoveryQuery(creds, "us-ashburn-1"))
	if err != nil {
		t.Fatalf("ListInstances falló: %v", err)
	}
	if calls != 2 {
		t.Fatalf("esperaba 2 llamadas (paginación), hubo %d", calls)
	}
	if len(results) != 2 {
		t.Fatalf("esperaba 2 instancias, hubo %d", len(results))
	}
	if results[0].ExternalID != "ocid1.instance.oc1..aaa" || results[0].Status != "running" {
		t.Errorf("primer resultado inesperado: %+v", results[0])
	}
	if results[1].ExternalID != "ocid1.instance.oc1..bbb" || results[1].Status != "stopped" {
		t.Errorf("segundo resultado inesperado: %+v", results[1])
	}
}

func TestListInstances_MissingCredentials(t *testing.T) {
	a := New()
	_, err := a.ListInstances(context.Background(), instancesDiscoveryQuery(apiKeyCredentials{}, "us-ashburn-1"))
	if err == nil {
		t.Fatal("esperaba error por credenciales faltantes")
	}
}

func TestCreateInstance_Success(t *testing.T) {
	fastPolling(t)
	creds, _ := testCreds(t)
	getInstanceCalls := 0

	withMockOCIServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/availabilityDomains"):
			_, _ = w.Write([]byte(`[{"name":"testAD:PHX-AD-1"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/20160918/instances":
			_, _ = w.Write([]byte(`{"id":"ocid1.instance.oc1..new","lifecycleState":"PROVISIONING"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/20160918/instances/"):
			getInstanceCalls++
			if getInstanceCalls < 2 {
				_, _ = w.Write([]byte(`{"id":"ocid1.instance.oc1..new","lifecycleState":"PROVISIONING"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"ocid1.instance.oc1..new","lifecycleState":"RUNNING"}`))
		default:
			t.Fatalf("request inesperado: %s %s", r.Method, r.URL.Path)
		}
	})

	a := New()
	spec := instanceSpecFor(creds, "us-ashburn-1", "ocid1.subnet.oc1..sub", "ocid1.image.oc1..img", "VM.Standard.E2.1.Micro")
	result, err := a.CreateInstance(context.Background(), spec)
	if err != nil {
		t.Fatalf("CreateInstance falló: %v", err)
	}
	if result.ExternalID != "ocid1.instance.oc1..new" || result.Status != "running" {
		t.Errorf("resultado inesperado: %+v", result)
	}
	if getInstanceCalls < 2 {
		t.Errorf("esperaba al menos 2 llamadas de polling, hubo %d", getInstanceCalls)
	}
}

func TestCreateInstance_MissingSubnet(t *testing.T) {
	creds, _ := testCreds(t)
	a := New()
	spec := instanceSpecFor(creds, "us-ashburn-1", "", "ocid1.image.oc1..img", "VM.Standard.E2.1.Micro")
	_, err := a.CreateInstance(context.Background(), spec)
	if err == nil {
		t.Fatal("esperaba error por falta de subnet_ext_id")
	}
}

func TestCreateInstance_TerminatesInsteadOfRunning(t *testing.T) {
	fastPolling(t)
	creds, _ := testCreds(t)

	withMockOCIServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/availabilityDomains"):
			_, _ = w.Write([]byte(`[{"name":"testAD:PHX-AD-1"}]`))
		case r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":"ocid1.instance.oc1..failed","lifecycleState":"PROVISIONING"}`))
		default:
			_, _ = w.Write([]byte(`{"id":"ocid1.instance.oc1..failed","lifecycleState":"TERMINATED"}`))
		}
	})

	a := New()
	spec := instanceSpecFor(creds, "us-ashburn-1", "ocid1.subnet.oc1..sub", "ocid1.image.oc1..img", "VM.Standard.E2.1.Micro")
	_, err := a.CreateInstance(context.Background(), spec)
	if err == nil {
		t.Fatal("esperaba error porque la instancia terminó en TERMINATED")
	}
}

// --- helpers para armar los tipos compartidos del paquete adapters sin
// repetir los mismos literales largos en cada test ---

func credentialsMap(creds apiKeyCredentials) map[string]string {
	return map[string]string{
		"user_ocid": creds.UserOCID, "tenancy_ocid": creds.TenancyOCID,
		"fingerprint": creds.Fingerprint, "private_key": creds.PrivateKey,
	}
}

func instancesDiscoveryQuery(creds apiKeyCredentials, region string) adapters.DiscoveryQuery {
	return adapters.DiscoveryQuery{Region: region, Credentials: credentialsMap(creds)}
}

func instanceSpecFor(creds apiKeyCredentials, region, subnet, image, shape string) adapters.InstanceSpec {
	return adapters.InstanceSpec{
		Name: "test-instance", Region: region, ShapeCode: shape, ImageID: image,
		SubnetExtID: subnet, AssignPublicIP: true,
		Credentials: credentialsMap(creds),
	}
}
