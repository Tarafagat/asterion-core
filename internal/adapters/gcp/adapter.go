// Package gcp implementa ProviderAdapter para Google Cloud. Discovery de
// instancias (ListInstances) y CreateInstance ya están cableados de verdad
// contra la API real de Compute Engine — ver auth.go para el flujo de
// autenticación y operations.go para la espera de la Operation asíncrona
// que devuelve instances.insert. El resto (CreateNetwork/CreateManagedDatabase/
// CreateBucket, ListNetworks/ListManagedDatabases/ListBuckets, GetCostReport)
// sigue como stub, mismo estado que internal/adapters/aws.
package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"asterion-core/internal/adapters"
	"asterion-core/internal/capabilities"
)

// computeReadonlyScope alcanza para listar instancias (GET) — ListInstances
// nunca crea/modifica nada, así que no necesita el scope de escritura.
const computeReadonlyScope = "https://www.googleapis.com/auth/compute.readonly"

// computeScope es el scope de lectura+escritura — lo necesita CreateInstance
// para poder llamar a instances.insert (compute.readonly lo rechazaría).
const computeScope = "https://www.googleapis.com/auth/compute"

// computeAPIBaseURL es var (no const) a propósito — mismo criterio que
// projectsURL en internal/adapters/vercel: los tests la apuntan a un
// httptest.Server y la restauran al terminar, sin tocar ninguna llamada
// real a Compute Engine.
var computeAPIBaseURL = "https://www.googleapis.com/compute/v1"

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Code() string { return "gcp" }

func (a *Adapter) Capabilities() capabilities.Set {
	return capabilities.NewSet(
		capabilities.Compute,
		capabilities.Network,
		capabilities.Subnet,
		capabilities.Firewall,
		capabilities.Storage,
		capabilities.Database,
		capabilities.PublicIP,
		capabilities.VPN,
		capabilities.IAM,
		capabilities.Pricing,
		capabilities.Discovery,
	)
}

// CreateInstance crea una VM real en Compute Engine. spec.Region se usa
// como ZONA (ej. "us-central1-a") — a propósito: InstanceSpec es un
// contrato compartido por los 5 adapters y no tiene un campo Zone
// dedicado; GCP es el único de los cinco donde este campo necesita ser
// una zona en vez de una región, porque instances.insert exige zona.
// spec.ImageID va tal cual como sourceImage (ej.
// "projects/debian-cloud/global/images/family/debian-12") — este adapter
// no resuelve alias de imagen, el caller manda la ruta completa de GCP.
//
// instances.insert no crea la VM al toque: devuelve una Operation
// asíncrona que hay que esperar (ver waitForZoneOperation en
// operations.go) — a diferencia de ListInstances, que es una sola
// llamada de lectura.
func (a *Adapter) CreateInstance(ctx context.Context, spec adapters.InstanceSpec) (adapters.InstanceResult, error) {
	serviceAccountJSON := spec.Credentials["service_account_json"]
	if serviceAccountJSON == "" {
		return adapters.InstanceResult{}, fmt.Errorf("gcp: falta 'service_account_json' en las credenciales")
	}
	key, err := parseServiceAccountKey(serviceAccountJSON)
	if err != nil {
		return adapters.InstanceResult{}, err
	}
	zone := spec.Region
	if zone == "" {
		return adapters.InstanceResult{}, fmt.Errorf("gcp: falta la zona (spec.region, ej. \"us-central1-a\")")
	}

	token, err := accessToken(ctx, key, computeScope)
	if err != nil {
		return adapters.InstanceResult{}, err
	}

	network := spec.NetworkExtID
	if network == "" {
		network = "global/networks/default"
	}

	type disk struct {
		Boot             bool `json:"boot"`
		AutoDelete       bool `json:"autoDelete"`
		InitializeParams struct {
			SourceImage string `json:"sourceImage"`
		} `json:"initializeParams"`
	}
	type accessConfig struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	type networkInterface struct {
		Network       string         `json:"network"`
		Subnetwork    string         `json:"subnetwork,omitempty"`
		AccessConfigs []accessConfig `json:"accessConfigs,omitempty"`
	}
	requestBody := struct {
		Name              string             `json:"name"`
		MachineType       string             `json:"machineType"`
		Disks             []disk             `json:"disks"`
		NetworkInterfaces []networkInterface `json:"networkInterfaces"`
	}{
		Name:        spec.Name,
		MachineType: fmt.Sprintf("zones/%s/machineTypes/%s", zone, spec.ShapeCode),
	}
	requestBody.Disks = []disk{{Boot: true, AutoDelete: true}}
	requestBody.Disks[0].InitializeParams.SourceImage = spec.ImageID
	iface := networkInterface{Network: network, Subnetwork: spec.SubnetExtID}
	if spec.AssignPublicIP {
		iface.AccessConfigs = []accessConfig{{Type: "ONE_TO_ONE_NAT", Name: "External NAT"}}
	}
	requestBody.NetworkInterfaces = []networkInterface{iface}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return adapters.InstanceResult{}, err
	}

	insertURL := fmt.Sprintf("%s/projects/%s/zones/%s/instances", computeAPIBaseURL, key.ProjectID, zone)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, insertURL, bytes.NewReader(payload))
	if err != nil {
		return adapters.InstanceResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return adapters.InstanceResult{}, fmt.Errorf("gcp: no se pudo conectar a Compute Engine: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return adapters.InstanceResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return adapters.InstanceResult{}, fmt.Errorf("gcp: Compute Engine respondió %d al crear la instancia: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var operation struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &operation); err != nil || operation.Name == "" {
		return adapters.InstanceResult{}, fmt.Errorf("gcp: no pude interpretar la Operation devuelta por instances.insert: %s", strings.TrimSpace(string(body)))
	}

	if err := waitForZoneOperation(ctx, token, key.ProjectID, zone, operation.Name); err != nil {
		return adapters.InstanceResult{}, err
	}

	return getInstance(ctx, token, key.ProjectID, zone, spec.Name)
}

// getInstance lee el estado real de la instancia recién creada — no se
// asume "RUNNING" solo porque la Operation terminó DONE (DONE significa
// "la API terminó de procesar la solicitud", no necesariamente que la VM
// ya esté corriendo — por ejemplo puede quedar en PROVISIONING/STAGING).
func getInstance(ctx context.Context, token, projectID, zone, name string) (adapters.InstanceResult, error) {
	requestURL := fmt.Sprintf("%s/projects/%s/zones/%s/instances/%s", computeAPIBaseURL, projectID, zone, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return adapters.InstanceResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return adapters.InstanceResult{}, fmt.Errorf("gcp: no se pudo conectar a Compute Engine: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return adapters.InstanceResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return adapters.InstanceResult{}, fmt.Errorf("gcp: Compute Engine respondió %d al leer la instancia recién creada: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return adapters.InstanceResult{}, fmt.Errorf("gcp: no pude interpretar la respuesta de Compute Engine: %w", err)
	}
	return adapters.InstanceResult{ExternalID: parsed.Name, Status: strings.ToLower(parsed.Status)}, nil
}

func (a *Adapter) CreateNetwork(ctx context.Context, spec adapters.NetworkSpec) (adapters.NetworkResult, error) {
	return adapters.NetworkResult{}, adapters.ErrNotImplemented
}

func (a *Adapter) CreateManagedDatabase(ctx context.Context, spec adapters.DatabaseSpec) (adapters.DatabaseResult, error) {
	return adapters.DatabaseResult{}, adapters.ErrNotImplemented
}

func (a *Adapter) CreateBucket(ctx context.Context, spec adapters.BucketSpec) (adapters.BucketResult, error) {
	return adapters.BucketResult{}, adapters.ErrNotImplemented
}

func (a *Adapter) ListInstances(ctx context.Context, q adapters.DiscoveryQuery) ([]adapters.InstanceResult, error) {
	serviceAccountJSON := q.Credentials["service_account_json"]
	if serviceAccountJSON == "" {
		return nil, fmt.Errorf("gcp: falta 'service_account_json' en las credenciales")
	}
	key, err := parseServiceAccountKey(serviceAccountJSON)
	if err != nil {
		return nil, err
	}

	token, err := accessToken(ctx, key, computeReadonlyScope)
	if err != nil {
		return nil, err
	}

	// aggregatedList trae las instancias de TODAS las zonas del proyecto en
	// una sola llamada — no hace falta iterar zona por zona. q.Region no se
	// usa para filtrar todavía (aggregatedList no tiene un filtro de región
	// directo, solo de zona vía el parámetro 'filter') — devuelve el
	// proyecto completo, igual que ListInstances de Vercel devuelve todos
	// los proyectos de la cuenta.
	requestURL := fmt.Sprintf("%s/projects/%s/aggregated/instances", computeAPIBaseURL, key.ProjectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gcp: no se pudo conectar a Compute Engine: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gcp: Compute Engine respondió %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Items map[string]struct {
			Instances []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"instances"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("gcp: no pude interpretar la respuesta de Compute Engine: %w", err)
	}

	results := make([]adapters.InstanceResult, 0)
	for _, zoneGroup := range parsed.Items {
		for _, inst := range zoneGroup.Instances {
			results = append(results, adapters.InstanceResult{
				ExternalID: inst.Name,
				Status:     strings.ToLower(inst.Status),
			})
		}
	}
	return results, nil
}

func (a *Adapter) ListNetworks(ctx context.Context, q adapters.DiscoveryQuery) ([]adapters.NetworkResult, error) {
	return nil, adapters.ErrNotImplemented
}

func (a *Adapter) ListManagedDatabases(ctx context.Context, q adapters.DiscoveryQuery) ([]adapters.DatabaseResult, error) {
	return nil, adapters.ErrNotImplemented
}

func (a *Adapter) ListBuckets(ctx context.Context, q adapters.DiscoveryQuery) ([]adapters.BucketResult, error) {
	return nil, adapters.ErrNotImplemented
}

func (a *Adapter) GetCostReport(ctx context.Context, q adapters.CostReportQuery) ([]adapters.CostLineItem, error) {
	return nil, adapters.ErrNotImplemented
}
