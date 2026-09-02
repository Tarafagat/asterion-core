// Package vercel implementa ProviderAdapter para Vercel.
//
// A diferencia de AWS/Azure/GCP/OCI (proveedores de infraestructura
// tradicional: VMs, redes, bases de datos gestionadas), Vercel es una
// plataforma de despliegue — no tiene VPCs/subnets/firewalls/bases de
// datos gestionadas en el sentido que modela este contrato, así que
// Capabilities() solo declara Compute (sus "proyectos" son lo más
// parecido a cómputo que ofrece) y Discovery. CreateInstance y las
// otras tres List* quedan ErrNotImplemented a propósito — no hay una
// forma honesta de "crear una instancia" en Vercel con este contrato
// pensado para VMs, y el pricing real de Vercel (facturación por uso,
// no por cpu/ram) tampoco se integra acá — mismo criterio que ya usan
// AWS/Azure/GCP/OCI con las capabilities que todavía no tienen el SDK
// real cableado, ver adapters.ErrNotImplemented.
//
// ListInstances sí llama a la API real de Vercel (GET /v9/projects,
// una de las rutas más estables y documentadas de su API — a diferencia
// de su API de uso/facturación, que no se integra acá). No se pudo
// probar de punta a punta contra una cuenta de Vercel real (no hay
// credenciales disponibles en este entorno de desarrollo) — sí está
// probado el armado de la request y el parseo de la respuesta contra
// un servidor de prueba que imita el shape documentado.
package vercel

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

// projectsURL es una var (no const) para poder apuntarla a un servidor de
// prueba en los tests, sin tocar la lógica del adapter.
var projectsURL = "https://api.vercel.com/v9/projects"

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Code() string { return "vercel" }

func (a *Adapter) Capabilities() capabilities.Set {
	return capabilities.NewSet(capabilities.Compute, capabilities.Discovery)
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

// ListInstances lista los proyectos de Vercel de la cuenta/equipo dueño de
// q.Credentials["token"] — el equivalente más cercano a "instancias" que
// tiene Vercel, aunque conceptualmente son proyectos de despliegue, no
// VMs. q.Credentials["team_id"] es opcional (un token que tiene acceso a
// varios equipos puede necesitarlo para filtrar a uno puntual). Status
// queda fijo en "active": un proyecto de Vercel no tiene un estado
// running/stopped como una VM — que exista ya es "activo".
func (a *Adapter) ListInstances(ctx context.Context, q adapters.DiscoveryQuery) ([]adapters.InstanceResult, error) {
	token := q.Credentials["token"]
	if token == "" {
		return nil, fmt.Errorf("vercel: falta 'token' en las credenciales")
	}

	url := projectsURL
	if teamID := q.Credentials["team_id"]; teamID != "" {
		url += "?teamId=" + teamID
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vercel: no se pudo conectar a la API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("vercel: la API respondió %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("vercel: no pude interpretar la respuesta: %w", err)
	}

	results := make([]adapters.InstanceResult, 0, len(parsed.Projects))
	for _, p := range parsed.Projects {
		results = append(results, adapters.InstanceResult{ExternalID: p.ID, Status: "active"})
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
