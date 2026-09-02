// asterion-core es el servicio que centraliza los Provider Adapters
// (AWS/Azure/GCP/OCI, y los que se agreguen después). Tanto el CLI como la
// API de Asterion hablan con este mismo servicio en vez de duplicar la
// integración con cada proveedor.
package main

import (
	"log"
	"net/http"
	"os"

	"asterion-core/internal/adapters"
	"asterion-core/internal/adapters/aws"
	"asterion-core/internal/adapters/azure"
	"asterion-core/internal/adapters/gcp"
	"asterion-core/internal/adapters/oci"
	"asterion-core/internal/adapters/vercel"
	"asterion-core/internal/coreserver"
)

func main() {
	registry := adapters.NewRegistry(aws.New(), azure.New(), gcp.New(), oci.New(), vercel.New())
	server := coreserver.New(registry)

	addr := os.Getenv("ASTERION_CORE_ADDR")
	if addr == "" {
		addr = ":8090"
	}

	log.Printf("asterion-core escuchando en %s (proveedores: %v)", addr, registry.Codes())
	if err := http.ListenAndServe(addr, server); err != nil {
		log.Fatal(err)
	}
}
