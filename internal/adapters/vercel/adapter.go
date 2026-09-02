// Package vercel implementa ProviderAdapter para Vercel.
//
// A diferencia de AWS/Azure/GCP/OCI (proveedores de infraestructura
// tradicional: VMs, redes, bases de datos gestionadas), Vercel es una
// plataforma de despliegue — no tiene VPCs/subnets/firewalls/bases de
// datos gestionadas en el sentido que modela este contrato, así que
// Capabilities() solo declara Compute (sus "proyectos" son lo más
// parecido a cómputo que ofrece), Discovery y Pricing. CreateInstance y
// las otras tres List* quedan ErrNotImplemented a propósito — no hay una
// forma honesta de "crear una instancia" en Vercel con este contrato
// pensado para VMs — mismo criterio que ya usan AWS/Azure/GCP/OCI con
// las capabilities que todavía no tienen el SDK real cableado, ver
// adapters.ErrNotImplemented.
//
// ListInstances sí llama a la API real de Vercel (GET /v9/projects,
// una de las rutas más estables y documentadas de su API). No se pudo
// probar de punta a punta contra una cuenta de Vercel real en el entorno
// de desarrollo original — sí está probado el armado de la request y el
// parseo de la respuesta contra un servidor de prueba que imita el shape
// documentado.
//
// GetCostReport también llama a la API real de Vercel: el endpoint FOCUS
// de facturación (GET /v1/billing/charges), que a diferencia de
// /v9/projects no devuelve un único JSON sino streaming JSONL (una línea
// = un cargo). Vercel no expone cpu/ram/storage — factura por uso — así
// que esto es lo más parecido a "pricing real" que tiene: costo en USD
// que Vercel ya calculó, no una estimación de Asterion.
package vercel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"asterion-core/internal/adapters"
	"asterion-core/internal/capabilities"
)

// projectsURL y billingChargesURL son var (no const) para poder apuntarlas
// a un servidor de prueba en los tests, sin tocar la lógica del adapter.
var projectsURL = "https://api.vercel.com/v9/projects"
var billingChargesURL = "https://api.vercel.com/v1/billing/charges"

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Code() string { return "vercel" }

func (a *Adapter) Capabilities() capabilities.Set {
	return capabilities.NewSet(capabilities.Compute, capabilities.Discovery, capabilities.Pricing)
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

// vercelCharge es una línea del JSONL que devuelve /v1/billing/charges
// (formato FOCUS v1.3). BilledCost viene documentado como number, pero el
// ejemplo oficial de Vercel lo muestra entre comillas ("123") — se decodifica
// como json.RawMessage y se interpreta con parseCostAmount para tolerar
// ambos casos sin romper si Vercel manda uno u otro.
type vercelCharge struct {
	BilledCost       json.RawMessage   `json:"BilledCost"`
	ChargeCategory   string            `json:"ChargeCategory"`
	ServiceName      string            `json:"ServiceName"`
	ConsumedQuantity json.RawMessage   `json:"ConsumedQuantity"`
	ConsumedUnit     string            `json:"ConsumedUnit"`
	Tags             map[string]string `json:"Tags"`
}

// parseCostAmount interpreta un campo numérico del FOCUS JSONL que puede
// venir como número JSON (123.45) o como string JSON ("123.45") — la propia
// documentación de Vercel es inconsistente entre el schema (number) y el
// ejemplo (string), así que esto no asume ninguno de los dos.
func parseCostAmount(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var asFloat float64
	if err := json.Unmarshal(raw, &asFloat); err == nil {
		return asFloat
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if v, err := strconv.ParseFloat(asString, 64); err == nil {
			return v
		}
	}
	return 0
}

// GetCostReport pide a Vercel el costo real ya facturado (no estimado) en
// el rango [q.From, q.To) vía el endpoint FOCUS de facturación. Cuando
// q.ExternalID no está vacío, filtra los cargos a ese proyecto puntual
// (Tags["ProjectId"]) — sin este filtro, el endpoint devuelve el gasto de
// TODO el equipo/cuenta, mezclando todos los proyectos. Los cargos se
// agrupan por ServiceName ("Edge Requests", "Fast Data Transfer", ...)
// sumando BilledCost, porque Vercel puede reportar varias líneas por día
// para el mismo servicio.
func (a *Adapter) GetCostReport(ctx context.Context, q adapters.CostReportQuery) ([]adapters.CostLineItem, error) {
	token := q.Credentials["token"]
	if token == "" {
		return nil, fmt.Errorf("vercel: falta 'token' en las credenciales")
	}
	if q.From == "" || q.To == "" {
		return nil, fmt.Errorf("vercel: 'from' y 'to' son obligatorios para el reporte de costos")
	}

	params := url.Values{}
	params.Set("from", q.From)
	params.Set("to", q.To)
	if teamID := q.Credentials["team_id"]; teamID != "" {
		params.Set("teamId", teamID)
	}
	requestURL := billingChargesURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vercel: no se pudo conectar a la API de facturación: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("vercel: la API de facturación respondió %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	totals := make(map[string]float64)
	order := make([]string, 0)
	scanner := bufio.NewScanner(resp.Body)
	// Las líneas de este endpoint pueden ser más largas que el buffer
	// default del Scanner (64KB) si Tags trae muchas claves.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var charge vercelCharge
		if err := json.Unmarshal([]byte(line), &charge); err != nil {
			// Una línea inentendible no debería tirar todo el reporte —
			// se ignora y se sigue con las demás, mismo criterio que
			// "nunca fabricar, pero tampoco fallar por un dato parcial".
			continue
		}
		if q.ExternalID != "" && charge.Tags["ProjectId"] != q.ExternalID {
			continue
		}
		if charge.ServiceName == "" {
			continue
		}
		if _, seen := totals[charge.ServiceName]; !seen {
			order = append(order, charge.ServiceName)
		}
		totals[charge.ServiceName] += parseCostAmount(charge.BilledCost)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("vercel: no pude leer la respuesta de facturación: %w", err)
	}

	results := make([]adapters.CostLineItem, 0, len(order))
	for _, service := range order {
		results = append(results, adapters.CostLineItem{ServiceName: service, BilledCostUSD: totals[service]})
	}
	return results, nil
}
