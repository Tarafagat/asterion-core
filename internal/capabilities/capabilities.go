// Package capabilities declara qué operaciones sabe hacer cada proveedor.
// El Core consulta esto ANTES de intentar una operación: si la capability
// no está declarada, ni siquiera se llama al adapter — se devuelve un error
// claro ("AWS no declara soporte para X") en vez de fallar a mitad de camino.
package capabilities

// Capability identifica una operación que un proveedor puede o no soportar
// (ej. "network", "pricing"). Los adapters declaran las suyas con NewSet;
// el Core las consulta con Set.Has antes de invocar al adapter.
type Capability string

// Capabilities conocidas por Asterion. Agregar una nueva acá la hace
// disponible para que cualquier adapter la declare y para que aparezca en
// `asterion capabilities <provider>` (ver All).
const (
	Compute  Capability = "compute"
	Network  Capability = "network"
	Subnet   Capability = "subnet"
	Firewall Capability = "firewall"
	Storage  Capability = "storage"
	Database Capability = "database"
	PublicIP Capability = "public_ip"
	VPN      Capability = "vpn"
	IAM      Capability = "iam"
	Pricing  Capability = "pricing"
	// Discovery: el proveedor puede, en principio, listar recursos que ya
	// existen en una cuenta conectada (no sólo crear recursos nuevos). Igual
	// que con las demás capabilities, declararla no implica que el SDK real
	// ya esté cableado — ver adapters.ErrNotImplemented.
	Discovery Capability = "discovery"
)

// All enumera las capabilities conocidas, en el orden en que se listan.
var All = []Capability{Compute, Network, Subnet, Firewall, Storage, Database, PublicIP, VPN, IAM, Pricing, Discovery}

// Set es la declaración de soporte de un proveedor: qué capabilities tiene.
type Set map[Capability]bool

// Has indica si el proveedor declara soporte para c.
func (s Set) Has(c Capability) bool {
	return s[c]
}

// NewSet arma un Set a partir de las capabilities soportadas. Las que no se
// pasan quedan implícitamente en false (no declaradas) — así es como OCI
// deja Pricing sin declarar: simplemente no la incluye acá.
func NewSet(supported ...Capability) Set {
	s := make(Set, len(supported))
	for _, c := range supported {
		s[c] = true
	}
	return s
}
