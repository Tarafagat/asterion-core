package adapters

import "fmt"

// Registry mapea el código de un proveedor (el mismo `providers.code` de la
// base de datos de Asterion: aws/azure/gcp/oci) a su ProviderAdapter. Un
// proveedor nuevo (DigitalOcean, Hetzner, Vultr, Alibaba, IBM...) se agrega
// registrándolo acá — nada más del Core, el CLI o la API necesita cambiar.
type Registry struct {
	byCode map[string]ProviderAdapter
}

// NewRegistry arma un Registry a partir de los adapters dados, indexados
// por su Code(). Se usa una sola vez, al arrancar cmd/asterion-core (ver
// ese main.go para el ejemplo real de cómo se registran los 4 proveedores
// actuales).
func NewRegistry(providers ...ProviderAdapter) *Registry {
	r := &Registry{byCode: make(map[string]ProviderAdapter, len(providers))}
	for _, p := range providers {
		r.byCode[p.Code()] = p
	}
	return r
}

// Get busca el adapter registrado para code (ej. "aws"). Devuelve error si
// no hay ninguno registrado con ese código.
func (r *Registry) Get(code string) (ProviderAdapter, error) {
	a, ok := r.byCode[code]
	if !ok {
		return nil, fmt.Errorf("no hay un adapter registrado para el proveedor %q", code)
	}
	return a, nil
}

// Codes lista los códigos de proveedor registrados (orden no garantizado).
func (r *Registry) Codes() []string {
	codes := make([]string, 0, len(r.byCode))
	for c := range r.byCode {
		codes = append(codes, c)
	}
	return codes
}
