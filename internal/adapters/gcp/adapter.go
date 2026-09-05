// Package gcp implementa ProviderAdapter para Google Cloud. Discovery de
// instancias (ListInstances) ya está cableado de verdad contra la API real
// de Compute Engine — ver auth.go para el flujo de autenticación. El resto
// (Create*, ListNetworks/ListManagedDatabases/ListBuckets, GetCostReport)
// sigue como stub, mismo estado que internal/adapters/aws.
package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"asterion-core/internal/adapters"
	"asterion-core/internal/capabilities"
)

// computeReadonlyScope alcanza para listar instancias (GET) — no se pide
// el scope de escritura ("compute", sin ".readonly") porque este adapter
// hoy solo hace discovery, nunca crea/modifica nada en GCP.
const computeReadonlyScope = "https://www.googleapis.com/auth/compute.readonly"

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

func (a *Adapter) CreateInstance(ctx context.Context, spec adapters.InstanceSpec) (adapters.InstanceResult, error) {
	return adapters.InstanceResult{}, adapters.ErrNotImplemented
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
	requestURL := fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/aggregated/instances", key.ProjectID)
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
