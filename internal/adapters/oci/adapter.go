// Package oci implementa ProviderAdapter para Oracle Cloud Infrastructure.
// Discovery de instancias (ListInstances) y CreateInstance ya están
// cableados de verdad contra la API real de Compute — ver auth.go para el
// esquema de firma propio de OCI (Request Signatures, RSA-SHA256, distinto
// del bearer token de GCP/Vercel) y poll.go para la espera hasta que la
// instancia recién lanzada llegue a RUNNING. El resto
// (CreateNetwork/CreateManagedDatabase/CreateBucket,
// ListNetworks/ListManagedDatabases/ListBuckets, GetCostReport) sigue como
// stub, mismo estado que internal/adapters/aws.
//
// A diferencia de AWS/Azure/GCP, OCI todavía no declara la capability de
// Pricing: no hay (todavía) una fuente de precios de OCI integrada al
// catálogo, así que el Core debe rechazar cualquier operación de pricing
// contra este proveedor en vez de inventar un precio.
package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"asterion-core/internal/adapters"
	"asterion-core/internal/capabilities"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Code() string { return "oci" }

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
		// Pricing deliberadamente NO declarada todavía.
		capabilities.Discovery,
	)
}

// CreateInstance lanza una VM real vía LaunchInstance. A diferencia de GCP
// (que tiene una red "default" implícita), OCI no crea nada de red por
// cuenta propia: spec.SubnetExtID es obligatorio, el OCID de una subnet ya
// existente en una VCN de la cuenta — sin eso, esto falla rápido en vez de
// adivinar una red. El availability domain no es un campo de InstanceSpec
// (es un detalle específico de OCI, realm-dependiente) así que se resuelve
// acá mismo pidiéndole a la API el primero disponible del compartment —
// suficiente para el caso común (una cuenta con pocos ADs por región); si
// ese AD no tiene capacidad para el shape pedido, LaunchInstance lo va a
// rechazar con un error claro, no hay reintento automático contra otro AD
// todavía.
func (a *Adapter) CreateInstance(ctx context.Context, spec adapters.InstanceSpec) (adapters.InstanceResult, error) {
	creds, err := parseCredentials(spec.Credentials)
	if err != nil {
		return adapters.InstanceResult{}, err
	}
	if spec.Region == "" {
		return adapters.InstanceResult{}, fmt.Errorf("oci: falta la región (spec.region, ej. \"us-ashburn-1\")")
	}
	if spec.ImageID == "" {
		return adapters.InstanceResult{}, fmt.Errorf("oci: falta spec.image_id (el OCID de la imagen a bootear)")
	}
	if spec.SubnetExtID == "" {
		return adapters.InstanceResult{}, fmt.Errorf("oci: falta spec.subnet_ext_id — OCI no tiene una red por defecto, hace falta el OCID de una subnet ya creada en una VCN de la cuenta")
	}

	compartmentID := resolveCompartmentID(spec.Credentials, creds)
	availabilityDomain, err := firstAvailabilityDomain(ctx, spec.Region, compartmentID, creds)
	if err != nil {
		return adapters.InstanceResult{}, err
	}

	type sourceDetails struct {
		SourceType string `json:"sourceType"`
		ImageID    string `json:"imageId"`
	}
	type createVnicDetails struct {
		SubnetID       string `json:"subnetId"`
		AssignPublicIP bool   `json:"assignPublicIp"`
	}
	requestBody := struct {
		CompartmentID      string            `json:"compartmentId"`
		AvailabilityDomain string            `json:"availabilityDomain"`
		Shape              string            `json:"shape"`
		DisplayName        string            `json:"displayName"`
		SourceDetails      sourceDetails     `json:"sourceDetails"`
		CreateVnicDetails  createVnicDetails `json:"createVnicDetails"`
	}{
		CompartmentID:      compartmentID,
		AvailabilityDomain: availabilityDomain,
		Shape:              spec.ShapeCode,
		DisplayName:        spec.Name,
		SourceDetails:      sourceDetails{SourceType: "image", ImageID: spec.ImageID},
		CreateVnicDetails:  createVnicDetails{SubnetID: spec.SubnetExtID, AssignPublicIP: spec.AssignPublicIP},
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return adapters.InstanceResult{}, err
	}

	launchURL := iaasBaseURL(spec.Region) + "/20160918/instances"
	body, status, _, err := doSigned(ctx, http.MethodPost, launchURL, creds, payload)
	if err != nil {
		return adapters.InstanceResult{}, err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return adapters.InstanceResult{}, fmt.Errorf("oci: Compute API respondió %d al crear la instancia: %s", status, strings.TrimSpace(string(body)))
	}

	var launched struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(body, &launched); err != nil {
		return adapters.InstanceResult{}, err
	}
	if launched.ID == "" {
		return adapters.InstanceResult{}, fmt.Errorf("oci: LaunchInstance no devolvió un id: %s", strings.TrimSpace(string(body)))
	}

	return waitForRunning(ctx, spec.Region, launched.ID, creds)
}

// firstAvailabilityDomain le pide a la Identity API los availability
// domains del compartment/región y devuelve el primero — ver el comentario
// de CreateInstance sobre por qué no hay selección más fina todavía.
func firstAvailabilityDomain(ctx context.Context, region, compartmentID string, creds apiKeyCredentials) (string, error) {
	listURL := fmt.Sprintf("%s/20160918/availabilityDomains?compartmentId=%s", identityBaseURL(region), url.QueryEscape(compartmentID))
	body, status, _, err := doSigned(ctx, http.MethodGet, listURL, creds, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("oci: Identity API respondió %d al listar availability domains: %s", status, strings.TrimSpace(string(body)))
	}
	var availabilityDomains []struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(body, &availabilityDomains); err != nil {
		return "", err
	}
	if len(availabilityDomains) == 0 {
		return "", fmt.Errorf("oci: el compartment no tiene ningún availability domain en %s", region)
	}
	return availabilityDomains[0].Name, nil
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

// ListInstances descubre instancias reales vía la ListInstances de Compute
// (no confundir con el método de este adapter que lleva el mismo nombre).
// q.Region es obligatorio (a diferencia de GCP, la API de OCI está
// particionada por región a nivel de host — "iaas.<region>.oraclecloud.com"
// — no hay un "aggregatedList" que traiga todas las regiones de una sola
// vez). Pagina vía el header de respuesta "opc-next-page" hasta agotarlo.
func (a *Adapter) ListInstances(ctx context.Context, q adapters.DiscoveryQuery) ([]adapters.InstanceResult, error) {
	creds, err := parseCredentials(q.Credentials)
	if err != nil {
		return nil, err
	}
	if q.Region == "" {
		return nil, fmt.Errorf("oci: falta la región (ej. \"us-ashburn-1\")")
	}
	compartmentID := resolveCompartmentID(q.Credentials, creds)

	results := make([]adapters.InstanceResult, 0)
	page := ""
	for {
		listURL := fmt.Sprintf("%s/20160918/instances?compartmentId=%s", iaasBaseURL(q.Region), url.QueryEscape(compartmentID))
		if page != "" {
			listURL += "&page=" + url.QueryEscape(page)
		}
		body, status, headers, err := doSigned(ctx, http.MethodGet, listURL, creds, nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("oci: Compute API respondió %d al listar instancias: %s", status, strings.TrimSpace(string(body)))
		}

		var items []struct {
			ID             string `json:"id"`
			LifecycleState string `json:"lifecycleState"`
		}
		if err := decodeJSON(body, &items); err != nil {
			return nil, err
		}
		for _, item := range items {
			results = append(results, adapters.InstanceResult{ExternalID: item.ID, Status: strings.ToLower(item.LifecycleState)})
		}

		page = headers.Get("opc-next-page")
		if page == "" {
			break
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
