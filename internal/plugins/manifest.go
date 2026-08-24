// Package plugins es el equivalente, para integraciones de terceros, de lo
// que internal/adapters es para los proveedores de nube: un contrato único
// que cualquiera puede implementar sin tocar el resto de Asterion. La
// diferencia es que un Provider Adapter es código Go compilado adentro del
// binario; un plugin es un PROCESO SEPARADO — su propia API HTTP local, en
// cualquier lenguaje — que Asterion instala (git clone), arranca, y
// administra desde afuera. Eso es deliberado: cargar código de un tercero
// adentro del mismo proceso (cgo plugin.so, o peor, un binario que se
// ejecuta con los mismos privilegios que el CLI) le daría a ese código
// acceso a todo lo que ya está en memoria — credenciales cloud descifradas
// incluidas. Un proceso aparte con su propio puerto es la misma frontera de
// aislamiento que ya usa el sistema operativo entre dos programas
// cualquiera, sin inventar nada nuevo.
//
// Cada plugin es un repo de git con un plugin.yaml en la raíz — el
// manifiesto, análogo a capabilities.Set: declara explícitamente qué
// necesita (config_schema) y cómo arrancarlo (start), nunca un script de
// instalación arbitrario. `asterion install plugin <url>` nunca ejecuta
// nada del repo salvo lo que este manifiesto declara.
//
// La definición del manifiesto en sí — el Asterion Plugin Contract — no
// vive acá: este paquete importa asterion-plugin-contract (repo hermano de
// este) y expone sus tipos como alias, para que el resto de asterion-core
// (install.go, process.go, config.go, store.go) siga usando los mismos
// nombres de siempre sin que exista una segunda definición del contrato en
// ningún lado.
package plugins

import apc "github.com/Tarafagat/asterion-plugin-contract/apc"

type (
	Manifest        = apc.Manifest
	StartSpec       = apc.StartSpec
	ConfigField     = apc.ConfigField
	LanguageSpec    = apc.LanguageSpec
	APISpec         = apc.APISpec
	PermissionsSpec = apc.PermissionsSpec
	ResourceSpec    = apc.ResourceSpec
	ActionSpec      = apc.ActionSpec
	EventsSpec      = apc.EventsSpec
)

// LoadManifest lee, defaultea y valida plugin.yaml en la raíz de dir —
// delega enteramente en apc.LoadManifest, la implementación de referencia
// del contrato.
func LoadManifest(dir string) (Manifest, error) {
	return apc.LoadManifest(dir)
}

// ValidateManifestDir hace lo mismo que LoadManifest y además confirma que
// los archivos que el manifiesto referencia por ruta relativa (api.openapi,
// resources[].schema) existen y son parseables — lo que corre
// `asterion plugin validate`.
func ValidateManifestDir(dir string) (Manifest, error) {
	return apc.ValidateDir(dir)
}
