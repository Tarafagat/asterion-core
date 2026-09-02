// Package aws implementa ProviderAdapter para Amazon Web Services.
//
// Todavía no llama al SDK real (aws-sdk-go-v2): no hay credenciales de AWS
// disponibles en este entorno para probar la integración de punta a punta,
// y publicar una llamada real sin probarla sería peor que no tenerla. Cada
// método hoy devuelve adapters.ErrNotImplemented — el contrato y las
// capabilities declaradas ya son reales y listas para que la implementación
// real se cablee acá adentro sin tocar el Core, el CLI ni la API.
package aws

import (
	"context"

	"asterion-core/internal/adapters"
	"asterion-core/internal/capabilities"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Code() string { return "aws" }

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
	return nil, adapters.ErrNotImplemented
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
