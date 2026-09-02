// Package adapters define el contrato único que implementa cada proveedor
// (AWS, Azure, GCP, OCI, y cualquiera que se agregue después: DigitalOcean,
// Hetzner, Vultr, Alibaba, IBM...). Ni el CLI ni la API de Asterion hablan
// directo con el SDK de un proveedor: ambos pasan por este mismo contrato,
// servido por asterion-core. Así la lógica de "cómo crear una instancia en
// AWS" existe en un solo lugar.
package adapters

import (
	"context"
	"errors"
	"fmt"

	"asterion-core/internal/capabilities"
)

// ErrNotImplemented: la capability está declarada pero el adapter todavía
// no tiene la llamada real al SDK del proveedor cableada (pendiente de
// credenciales reales para probarse de punta a punta).
var ErrNotImplemented = errors.New("operación no implementada todavía en este adapter")

// ErrCapabilityNotSupported: el proveedor directamente no declara soporte
// para esta operación. El Core nunca intenta la llamada en este caso.
type ErrCapabilityNotSupported struct {
	Provider   string
	Capability capabilities.Capability
}

func (e *ErrCapabilityNotSupported) Error() string {
	return fmt.Sprintf("%s no declara soporte para la capability %q", e.Provider, e.Capability)
}

// InstanceSpec describe la instancia de cómputo a crear. Credentials viaja
// descifrado en este punto (el backend de Asterion lo descifra antes de
// llamar a Core) y nunca se loguea.
type InstanceSpec struct {
	Name           string            `json:"name"`
	Region         string            `json:"region"`
	ShapeCode      string            `json:"shape_code"`
	ImageID        string            `json:"image_id"`
	NetworkExtID   string            `json:"network_ext_id,omitempty"`
	SubnetExtID    string            `json:"subnet_ext_id,omitempty"`
	AssignPublicIP bool              `json:"assign_public_ip"`
	Credentials    map[string]string `json:"credentials"`
}

// InstanceResult es lo que devuelve el proveedor tras crear la instancia:
// el identificador real en su lado (ExternalID) y su estado reportado.
type InstanceResult struct {
	ExternalID string `json:"external_id"`
	Status     string `json:"status"`
}

// NetworkSpec describe la red (VPC/VNet/VCN) a crear.
type NetworkSpec struct {
	Name        string            `json:"name"`
	Region      string            `json:"region"`
	CIDRBlock   string            `json:"cidr_block"`
	Credentials map[string]string `json:"credentials"`
}

// NetworkResult es el resultado de crear una red en el proveedor.
type NetworkResult struct {
	ExternalID string `json:"external_id"`
	Status     string `json:"status"`
}

// DatabaseSpec describe la base de datos gestionada a crear.
type DatabaseSpec struct {
	Name          string            `json:"name"`
	Region        string            `json:"region"`
	Engine        string            `json:"engine"`
	EngineVersion string            `json:"engine_version"`
	InstanceClass string            `json:"instance_class"`
	StorageGB     int               `json:"storage_gb"`
	MultiAZ       bool              `json:"multi_az"`
	Credentials   map[string]string `json:"credentials"`
}

// DatabaseResult es el resultado de crear una base de datos en el proveedor.
type DatabaseResult struct {
	ExternalID string `json:"external_id"`
	Status     string `json:"status"`
}

// BucketSpec describe el bucket de almacenamiento de objetos a crear.
type BucketSpec struct {
	Name        string            `json:"name"`
	Region      string            `json:"region"`
	Credentials map[string]string `json:"credentials"`
}

// BucketResult es el resultado de crear un bucket en el proveedor.
type BucketResult struct {
	ExternalID string `json:"external_id"`
	Status     string `json:"status"`
}

// DiscoveryQuery describe contra qué cuenta/región listar recursos que ya
// existen en el proveedor (a diferencia de los Spec de arriba, que describen
// algo a crear). Credentials viaja descifrado, igual que en los Spec.
type DiscoveryQuery struct {
	Region      string            `json:"region"`
	Credentials map[string]string `json:"credentials"`
}

// CostReportQuery describe el rango de fechas (ISO 8601) sobre el que pedir
// costo real de uso ya facturado por el proveedor. ExternalID, cuando no
// está vacío, filtra el reporte a un único recurso (ej. un proyecto de
// Vercel) — sin él, algunos proveedores devolverían el gasto de toda la
// cuenta/equipo, no de una instancia puntual.
type CostReportQuery struct {
	From        string            `json:"from"`
	To          string            `json:"to"`
	ExternalID  string            `json:"external_id,omitempty"`
	Credentials map[string]string `json:"credentials"`
}

// CostLineItem es un renglón de costo real ya facturado por el proveedor
// para un servicio/recurso puntual dentro del rango de CostReportQuery (ej.
// "Edge Requests" en Vercel). BilledCostUSD sale del proveedor, nunca se
// calcula localmente — a diferencia de InstanceResult, esto no describe un
// recurso sino un cargo.
type CostLineItem struct {
	ServiceName      string  `json:"service_name"`
	BilledCostUSD    float64 `json:"billed_cost_usd"`
	ConsumedQuantity float64 `json:"consumed_quantity,omitempty"`
	ConsumedUnit     string  `json:"consumed_unit,omitempty"`
}

// ProviderAdapter es el contrato que cada proveedor debe cumplir para que
// asterion-core pueda operarlo. Code identifica al proveedor (debe
// coincidir con `providers.code` en la base de datos de Asterion: aws,
// azure, gcp, oci, ...). Capabilities declara qué de esto soporta de
// verdad — ver el paquete capabilities. Los métodos Create* reciben el spec
// ya validado por la API de Asterion; devolver ErrNotImplemented es válido
// mientras el adapter no tenga la llamada real al SDK del proveedor.
type ProviderAdapter interface {
	Code() string
	Capabilities() capabilities.Set
	CreateInstance(ctx context.Context, spec InstanceSpec) (InstanceResult, error)
	CreateNetwork(ctx context.Context, spec NetworkSpec) (NetworkResult, error)
	CreateManagedDatabase(ctx context.Context, spec DatabaseSpec) (DatabaseResult, error)
	CreateBucket(ctx context.Context, spec BucketSpec) (BucketResult, error)
	// ListInstances, ListNetworks, ListManagedDatabases y ListBuckets listan
	// recursos que ya existen en la cuenta del proveedor (descubrimiento),
	// simétrico a los Create* de arriba pero de solo lectura. Devolver
	// ErrNotImplemented es válido mientras el adapter no tenga la llamada
	// real al SDK del proveedor cableada — igual que los Create*.
	ListInstances(ctx context.Context, q DiscoveryQuery) ([]InstanceResult, error)
	ListNetworks(ctx context.Context, q DiscoveryQuery) ([]NetworkResult, error)
	ListManagedDatabases(ctx context.Context, q DiscoveryQuery) ([]DatabaseResult, error)
	ListBuckets(ctx context.Context, q DiscoveryQuery) ([]BucketResult, error)
	// GetCostReport devuelve costo real ya facturado por el proveedor
	// (capability Pricing) — a diferencia de calculate_cost del lado de
	// Asterion Cloud, que ESTIMA a partir de cpu/ram/storage, esto son
	// cargos reales que el proveedor ya calculó. Devolver ErrNotImplemented
	// es válido mientras el adapter no tenga esto cableado.
	GetCostReport(ctx context.Context, q CostReportQuery) ([]CostLineItem, error)
}

// RequireCapability es el gate que usa el Core antes de invocar un adapter:
// si el proveedor no declaró la capability, ni siquiera se llama al adapter.
func RequireCapability(a ProviderAdapter, c capabilities.Capability) error {
	if !a.Capabilities().Has(c) {
		return &ErrCapabilityNotSupported{Provider: a.Code(), Capability: c}
	}
	return nil
}
