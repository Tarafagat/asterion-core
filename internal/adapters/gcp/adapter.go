// Package gcp implementa ProviderAdapter para Google Cloud.
// Ver el comentario de paquete en internal/adapters/aws — mismo estado.
package gcp

import (
	"context"

	"asterion-core/internal/adapters"
	"asterion-core/internal/capabilities"
)

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
