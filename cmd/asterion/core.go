package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"asterion-core/internal/adapters"
	"asterion-core/internal/adapters/aws"
	"asterion-core/internal/adapters/azure"
	"asterion-core/internal/adapters/gcp"
	"asterion-core/internal/adapters/oci"
	"asterion-core/internal/coreserver"
)

// coreCmd agrupa el servicio de Provider Adapters (AWS/Azure/GCP/OCI) bajo
// el mismo binario `asterion` — mismo criterio que `local serve` para el
// dashboard: un solo binario para todo lo que un operador necesita correr,
// en vez de tener que acordarse de un segundo ejecutable (`asterion-core`,
// `cmd/asterion-core`) aparte. Ese binario standalone sigue existiendo (útil
// para, por ejemplo, una imagen de contenedor mínima con solo este
// servicio) — este subcomando es la misma lógica, expuesta también acá.
func coreCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "core",
		Short: "Servicio de Provider Adapters (AWS/Azure/GCP/OCI) — lo consumen el CLI y Asterion Cloud (CORE_SERVICE_URL)",
	}
	root.AddCommand(coreServeCmd())
	return root
}

func coreServeCmd() *cobra.Command {
	defaultAddr := os.Getenv("ASTERION_CORE_ADDR")
	if defaultAddr == "" {
		defaultAddr = ":8090"
	}
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Levanta el servicio de Provider Adapters en primer plano",
		Long: "Expone por HTTP los 4 Provider Adapters (AWS/Azure/GCP/OCI) — capabilities, discovery\n" +
			"y las operaciones de aprovisionamiento que ya estén implementadas. Es lo que 'asterion\n" +
			"providers'/'asterion capabilities' consultan localmente, y lo mismo que Asterion Cloud\n" +
			"espera en CORE_SERVICE_URL. Corre en primer plano a propósito (Ctrl-C para parar) —\n" +
			"para dejarlo corriendo de verdad, administralo con systemd/tu gestor de procesos, igual\n" +
			"que cualquier otro servicio (ver el README, sección 'Levantar asterion-core').",
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := adapters.NewRegistry(aws.New(), azure.New(), gcp.New(), oci.New())
			server := coreserver.New(registry)
			fmt.Printf("asterion-core escuchando en %s (proveedores: %v)\n", addr, registry.Codes())
			return http.ListenAndServe(addr, server)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", defaultAddr, "Dirección donde escuchar (default también configurable con ASTERION_CORE_ADDR)")
	return cmd
}
