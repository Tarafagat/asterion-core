// Package cloudmeta detecta, desde ADENTRO de la máquina donde corre el
// agente, si esta es una VM real de algún proveedor cloud soportado —
// preguntándole al servicio de metadata que cada uno expone en su propia
// máquina (nunca sale a internet, siempre la IP link-local 169.254.169.254
// o el hostname interno de GCP). Sirve para que Asterion Cloud pueda
// correlacionar una instancia conectada por agente con el mismo recurso
// descubierto después desde una cuenta cloud, en vez de duplicarla (ver
// cmd/asterion/agent.go::reportHeartbeat y el plan de la sesión).
//
// Provider == "" es un resultado tan válido como cualquier otro: en un
// server privado/bare-metal, los cuatro proveedores fallan (no hay ruta a
// esas direcciones desde ahí) y Detect simplemente no encuentra nada — el
// agente sigue funcionando exactamente igual que si este paquete no
// existiera, nunca es un error fatal.
package cloudmeta

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Identity es lo que se pudo confirmar sobre el proveedor cloud en el que
// corre esta máquina. NativeID es el identificador que ESE proveedor usa
// para nombrar el recurso — el mismo valor que después aparece como
// external_id al descubrir recursos de una cuenta cloud conectada, para
// que ambos se puedan cruzar.
type Identity struct {
	Provider string `json:"cloud_provider,omitempty"`
	NativeID string `json:"cloud_native_id,omitempty"`
}

// probeTimeout es corto a propósito: en la nube correcta, el servicio de
// metadata responde en milisegundos (es de la misma red virtual); en
// cualquier otro lado (las otras 3 nubes, o un server privado) lo que hay
// del otro lado es silencio de red — sin esto, un timeout default de
// net/http (sin límite) dejaría 'agent-run' colgado varios segundos por
// cada intento fallido.
const probeTimeout = 800 * time.Millisecond

// probe es la forma común de cada detector: intenta confirmar que esta
// máquina es una VM de su proveedor, devuelve el id nativo si pudo.
type probe func(ctx context.Context) (nativeID string, ok bool)

// Detect prueba, en orden, el servicio de metadata de cada proveedor
// soportado — se detiene en el primero que responde. Se llama UNA sola
// vez al arrancar 'agent-run' (ver cmd/asterion/agent.go), nunca en cada
// heartbeat: el resultado no cambia mientras el proceso sigue vivo.
func Detect(ctx context.Context) Identity {
	providers := []struct {
		name  string
		probe probe
	}{
		{"gcp", probeGCP},
		{"aws", probeAWS},
		{"azure", probeAzure},
		{"oci", probeOCI},
	}
	for _, p := range providers {
		if id, ok := p.probe(ctx); ok && id != "" {
			return Identity{Provider: p.name, NativeID: id}
		}
	}
	return Identity{}
}

func doRequest(ctx context.Context, method, url string, headers map[string]string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, false
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return nil, false
	}
	return body, true
}

// probeGCP: metadata.google.internal (hostname interno, solo resuelve
// dentro de una VM de GCP) — /instance/name en vez de /instance/id porque
// es el mismo valor que ya devuelve internal/adapters/gcp::ListInstances
// como ExternalID (el nombre, no el id numérico interno).
func probeGCP(ctx context.Context) (string, bool) {
	body, ok := doRequest(ctx, http.MethodGet,
		"http://metadata.google.internal/computeMetadata/v1/instance/name",
		map[string]string{"Metadata-Flavor": "Google"},
	)
	if !ok {
		return "", false
	}
	name := strings.TrimSpace(string(body))
	return name, name != ""
}

// probeAWS: IMDSv2 (con token — IMDSv1, sin token, está deprecado por
// AWS) — dos pasos: pedir un token de corta vida, usarlo para leer
// instance-id.
func probeAWS(ctx context.Context) (string, bool) {
	tokenBody, ok := doRequest(ctx, http.MethodPut,
		"http://169.254.169.254/latest/api/token",
		map[string]string{"X-aws-ec2-metadata-token-ttl-seconds": "60"},
	)
	if !ok {
		return "", false
	}
	token := strings.TrimSpace(string(tokenBody))
	if token == "" {
		return "", false
	}

	idBody, ok := doRequest(ctx, http.MethodGet,
		"http://169.254.169.254/latest/meta-data/instance-id",
		map[string]string{"X-aws-ec2-metadata-token": token},
	)
	if !ok {
		return "", false
	}
	id := strings.TrimSpace(string(idBody))
	return id, id != ""
}

// probeAzure: Azure Instance Metadata Service — la respuesta es un JSON
// grande, solo nos interesa compute.name (mismo campo que Azure muestra
// como nombre de la VM).
func probeAzure(ctx context.Context) (string, bool) {
	body, ok := doRequest(ctx, http.MethodGet,
		"http://169.254.169.254/metadata/instance?api-version=2021-02-01",
		map[string]string{"Metadata": "true"},
	)
	if !ok {
		return "", false
	}
	var parsed struct {
		Compute struct {
			Name string `json:"name"`
		} `json:"compute"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", false
	}
	name := strings.TrimSpace(parsed.Compute.Name)
	return name, name != ""
}

// probeOCI: OCI Instance Metadata Service v2 — exige el header
// Authorization: Bearer Oracle (literal, no es un token real) en todas
// las llamadas a /opc/v2/. displayName es el nombre visible de la
// instancia, igual que en la consola de OCI.
func probeOCI(ctx context.Context) (string, bool) {
	body, ok := doRequest(ctx, http.MethodGet,
		"http://169.254.169.254/opc/v2/instance/",
		map[string]string{"Authorization": "Bearer Oracle"},
	)
	if !ok {
		return "", false
	}
	var parsed struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", false
	}
	name := strings.TrimSpace(parsed.DisplayName)
	return name, name != ""
}
