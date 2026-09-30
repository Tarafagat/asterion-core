// Package pluginsvc resuelve los servicios EXTERNOS que un plugin
// declara en su plugin.yaml (una base de datos, un Redis — ver
// apc.ServiceSpec) y vuelca los datos de conexión en la config cifrada
// de ese plugin, para que nadie tenga que crear la base a mano y después
// copiar host/puerto/usuario/contraseña uno por uno.
//
// El orden es siempre el mismo, y es deliberado:
//
//  1. DETECTAR: ¿ya hay un motor de ese tipo funcionando y alcanzable?
//     Si la config del plugin ya apunta a uno que responde, no se toca
//     nada — "si ya hay una que funcione, usala".
//  2. CONFIGURAR DENTRO de lo que existe: crear la base y el usuario que
//     falten, con una contraseña generada, y volcar la conexión.
//  3. Recién si NO hay motor, y SOLO si se pide explícitamente, levantar
//     uno en un contenedor. Nunca por su cuenta: arrancar un contenedor
//     que nadie pidió es exactamente el tipo de sorpresa que este
//     proyecto evita.
//
// Se habla con cada motor por su CLI (psql/mysql/redis-cli), mismo
// criterio que internal/dbbackup con pg_dump/mysqldump: cero
// dependencias Go nuevas, y funciona exactamente donde ya funciona la
// herramienta que un administrador de esa base ya tiene. Si el CLI no
// está, se dice — no se finge un chequeo que no se hizo.
package pluginsvc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Tarafagat/asterion-plugin-contract/apc"
)

// Kinds soportados.
const (
	KindPostgres = "postgres"
	KindMySQL    = "mysql"
	KindMariaDB  = "mariadb"
	KindRedis    = "redis"
)

// defaultPort es el puerto estándar de cada motor — el primer lugar
// donde se busca uno ya corriendo.
func defaultPort(kind string) int {
	switch kind {
	case KindPostgres:
		return 5432
	case KindMySQL, KindMariaDB:
		return 3306
	case KindRedis:
		return 6379
	}
	return 0
}

// cliFor es el cliente de línea de comandos con el que se habla con cada
// motor.
func cliFor(kind string) string {
	switch kind {
	case KindPostgres:
		return "psql"
	case KindMySQL, KindMariaDB:
		return "mysql"
	case KindRedis:
		return "redis-cli"
	}
	return ""
}

// Status es lo que se sabe de UN servicio declarado, sin cambiar nada —
// el resultado de la fase de detección.
type Status struct {
	Service apc.ServiceSpec `json:"service"`

	// Configured: la config del plugin YA tiene los datos de conexión.
	Configured bool `json:"configured"`
	// Reachable: hay algo escuchando y respondiendo en ese host:puerto.
	Reachable bool `json:"reachable"`
	// CLIAvailable: está el cliente (psql/mysql/redis-cli) para poder
	// crear la base/usuario. Sin él se puede detectar por TCP pero no
	// configurar nada adentro.
	CLIAvailable bool `json:"cli_available"`

	Host string `json:"host"`
	Port int    `json:"port"`

	// BadPort es el valor guardado en la clave maps_port cuando no es un
	// puerto válido. Se conserva para poder decirlo en vez de volver al
	// estándar sin avisar.
	BadPort string `json:"bad_port,omitempty"`

	// Container, si no está vacío, es el contenedor que Asterion levantó
	// para este servicio. Cambia por dónde se le habla al motor: adentro
	// del contenedor el cliente ya viene instalado, así que no hace falta
	// tenerlo en el host (ver engineRunner).
	Container string `json:"container,omitempty"`

	// Detail explica en una línea qué se encontró — lo que el CLI
	// imprime al lado de cada servicio.
	Detail string `json:"detail"`
}

// Ready responde si este servicio no necesita ninguna acción. Un puerto
// guardado inválido nunca cuenta como listo, aunque el estándar responda:
// lo que quedó en la config no es lo que se está probando.
func (s Status) Ready() bool { return s.Configured && s.Reachable && s.BadPort == "" }

// Detect mira el estado de un servicio declarado SIN tocar nada: si la
// config del plugin ya apunta a algún lado, prueba ahí; si no, prueba en
// el puerto estándar de ese motor en localhost.
func Detect(spec apc.ServiceSpec, pluginName string, config map[string]string) Status {
	st := Status{Service: spec}

	st.Host = firstNonEmpty(config[spec.MapsHost], "127.0.0.1")
	st.Port = defaultPort(spec.Kind)
	// Un puerto guardado que no es un número no se puede ignorar y seguir
	// como si nada: se volvería al estándar, el plugin le hablaría a otro
	// servidor y este reporte diría que está todo bien. Se avisa.
	if p := strings.TrimSpace(config[spec.MapsPort]); p != "" {
		n, err := strconv.Atoi(p)
		switch {
		case err != nil || n < 1 || n > 65535:
			st.BadPort = p
		default:
			st.Port = n
		}
	}

	// "Configurado" = están los datos que el propio manifiesto pidió
	// mapear. Un maps_* que el plugin no declaró no cuenta como faltante.
	st.Configured = true
	for _, key := range []string{spec.MapsHost, spec.MapsUser, spec.MapsPassword, spec.MapsDatabase, spec.MapsURL} {
		if key != "" && strings.TrimSpace(config[key]) == "" {
			st.Configured = false
			break
		}
	}

	st.Reachable = tcpReachable(st.Host, st.Port)
	if cli := cliFor(spec.Kind); cli != "" {
		_, err := exec.LookPath(cli)
		st.CLIAvailable = err == nil
	}

	// Si el motor responde pero el cliente no está en el host, todavía
	// puede haber camino: que ese motor sea un contenedor que Asterion
	// levantó antes, con su cliente adentro. Se consulta solo en ese caso
	// —el único donde cambia algo— para no pagar un 'docker inspect' en
	// cada detección.
	if st.Reachable && !st.CLIAvailable {
		st.Container = runningAsterionContainer(pluginName, spec.Name, st.Port)
	}

	switch {
	case st.BadPort != "":
		st.Detail = fmt.Sprintf("la clave %s tiene %q guardado, que no es un puerto — corregilo con 'asterion plugin services connect' antes de seguir",
			spec.MapsPort, st.BadPort)
	case st.Configured && st.Reachable:
		st.Detail = fmt.Sprintf("configurado y respondiendo en %s:%d", st.Host, st.Port)
	case st.Configured && !st.Reachable:
		st.Detail = fmt.Sprintf("configurado a %s:%d, pero ahí no responde nada", st.Host, st.Port)
	case st.Reachable && st.Container != "":
		st.Detail = fmt.Sprintf("hay un %s en %s:%d, en el contenedor %q que levantó Asterion — se puede configurar desde adentro",
			spec.Kind, st.Host, st.Port, st.Container)
	case st.Reachable && !st.CLIAvailable:
		st.Detail = fmt.Sprintf("hay un %s en %s:%d, pero falta el cliente %q para poder configurarlo",
			spec.Kind, st.Host, st.Port, cliFor(spec.Kind))
	case st.Reachable:
		st.Detail = fmt.Sprintf("hay un %s escuchando en %s:%d — se puede usar ese", spec.Kind, st.Host, st.Port)
	default:
		st.Detail = fmt.Sprintf("no hay ningún %s en %s:%d", spec.Kind, st.Host, st.Port)
	}
	return st
}

// tcpReachable es la detección más honesta que se puede hacer sin
// credenciales: ¿hay algo aceptando conexiones ahí? No dice que sea el
// motor correcto ni que las credenciales sirvan — eso recién se sabe al
// intentar usarlo, y se reporta entonces.
func tcpReachable(host string, port int) bool {
	if port == 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 1500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// StoredPassword busca la contraseña que la config del plugin ya tiene para
// este servicio. Mira dos lugares porque hay dos formas válidas de que un
// manifiesto la guarde:
//
//   - maps_password, una clave propia — el caso directo;
//   - maps_url, cuando el plugin solo quiere una URL de conexión (muy común:
//     una sola variable DATABASE_URL y nada más). La contraseña está ahí
//     adentro, y es la misma. Sin leerla de ahí, un servicio declarado solo
//     con maps_url pediría --rotate-password en cada corrida, porque
//     parecería que nunca hubo contraseña guardada.
func StoredPassword(spec apc.ServiceSpec, config map[string]string) string {
	if pw := config[spec.MapsPassword]; pw != "" {
		return pw
	}
	if spec.MapsURL == "" {
		return ""
	}
	u, err := url.Parse(config[spec.MapsURL])
	if err != nil || u.User == nil {
		return ""
	}
	pw, _ := u.User.Password()
	return pw
}

// AdminCreds son las credenciales con las que se entra al motor YA
// EXISTENTE para crear la base y el usuario del plugin. Nunca se
// guardan: se usan en el momento y se descartan — lo único que queda
// persistido es el usuario nuevo del plugin, en su config cifrada.
type AdminCreds struct {
	User     string
	Password string
}

// Resolution es lo que quedó resuelto para un servicio: los valores que
// se van a volcar a la config del plugin.
type Resolution struct {
	Service  apc.ServiceSpec   `json:"service"`
	Host     string            `json:"host"`
	Port     int               `json:"port"`
	User     string            `json:"user,omitempty"`
	Password string            `json:"-"` // nunca se serializa
	Database string            `json:"database,omitempty"`
	URL      string            `json:"-"` // contiene la contraseña
	Values   map[string]string `json:"-"` // lo que se escribe en la config del plugin
	Actions  []string          `json:"actions"`
}

// Provision asegura, DENTRO de un motor que ya existe y responde, la
// base y el usuario que el plugin declaró — creando lo que falte y
// dejando lo que ya esté. Devuelve los valores listos para volcar a la
// config del plugin.
//
// Idempotente a propósito: correrlo dos veces no rompe nada ni rota la
// contraseña de un usuario que ya existía (rotarla en silencio dejaría
// al plugin con una credencial vieja en su config).
// ProvisionOptions son las decisiones que Provision no puede tomar solo.
type ProvisionOptions struct {
	// Admin son las credenciales del motor ya existente. No se guardan.
	Admin AdminCreds

	// ExistingPassword es la contraseña que la config del plugin YA tiene
	// para este servicio, si tiene alguna. Se reusa en vez de rotarla.
	ExistingPassword string

	// RotatePassword autoriza a cambiarle la contraseña a un usuario que ya
	// existe. Es explícito y nunca el default: si otra cosa está usando ese
	// mismo usuario, rotarla la deja afuera.
	RotatePassword bool
}

func Provision(ctx context.Context, spec apc.ServiceSpec, st Status, opts ProvisionOptions) (*Resolution, error) {
	admin, existingPassword := opts.Admin, opts.ExistingPassword
	if !st.Reachable {
		return nil, fmt.Errorf("no hay ningún %s respondiendo en %s:%d — configuralo a mano, apuntalo a uno remoto, o pedí explícitamente que se levante uno",
			spec.Kind, st.Host, st.Port)
	}
	// Cómo se le habla al motor. Si el contenedor es de Asterion, se usa el
	// cliente que ya viene adentro: así 'services up --create' funciona en
	// una máquina que no tiene psql instalado, que es el caso normal en
	// macOS. Solo se exige el cliente en el host cuando hay que hablarle a
	// un motor que Asterion no levantó.
	r := hostRunner(st)
	if st.Container != "" {
		r = containerRunner(st.Container, spec.Kind)
	} else if cli := cliFor(spec.Kind); cli != "" && !st.CLIAvailable {
		return nil, fmt.Errorf("falta %q en el PATH — es con lo que Asterion le habla a un %s que no levantó él; instalalo, o configurá este servicio a mano con 'asterion plugin services connect'", cli, spec.Kind)
	}

	// Lo que se vuelca a la config es SIEMPRE la dirección del host: es por
	// ahí que el plugin va a conectarse, no por el puerto interno del
	// contenedor que usa el runner.
	res := &Resolution{Service: spec, Host: st.Host, Port: st.Port, Values: map[string]string{}}

	switch spec.Kind {
	case KindRedis:
		// A un Redis no se le "crea" nada: no tiene usuarios ni bases que
		// aprovisionar. Lo único honesto que se puede hacer es comprobar
		// que responde con la credencial que ya haya guardada (si el
		// manifiesto declaró dónde guardarla) y volcar host/puerto.
		res.Password = existingPassword
		if err := pingRedis(ctx, r, spec, res.Password); err != nil {
			return nil, err
		}
		res.Actions = append(res.Actions, fmt.Sprintf("redis en %s:%d respondió PONG", st.Host, st.Port))

	case KindPostgres, KindMySQL, KindMariaDB:
		res.User = firstNonEmpty(spec.User, spec.Name)
		res.Database = firstNonEmpty(spec.Database, spec.Name)
		// Una contraseña ya guardada NO se rota: el usuario podría
		// existir con esa misma clave, y cambiarla acá dejaría al plugin
		// (o a otro servicio) con la vieja.
		res.Password = existingPassword
		if res.Password == "" {
			pw, err := generatePassword()
			if err != nil {
				return nil, err
			}
			res.Password = pw
			res.Actions = append(res.Actions, "contraseña generada para el usuario del plugin")
		}

		var err error
		if spec.Kind == KindPostgres {
			err = provisionPostgres(ctx, r, admin, opts, res)
		} else {
			err = provisionMySQL(ctx, r, admin, opts, res)
		}
		if err != nil {
			return nil, err
		}

		// Recién acá se sabe si lo que se va a guardar sirve. Entrar con la
		// credencial del plugin es el único chequeo que lo prueba.
		if err := verifyLogin(ctx, r, spec.Kind, res); err != nil {
			return nil, err
		}
		res.Actions = append(res.Actions, fmt.Sprintf("verificado: %q entra a %q con la credencial que se guarda", res.User, res.Database))

	default:
		return nil, fmt.Errorf("kind %q no soportado", spec.Kind)
	}

	res.URL = buildURL(spec.Kind, res)
	fillValues(spec, res)
	return res, nil
}

// fillValues traduce la resolución a las claves del config_schema que el
// manifiesto pidió mapear — solo las declaradas, nunca inventa claves.
func fillValues(spec apc.ServiceSpec, res *Resolution) {
	set := func(key, value string) {
		if key != "" && value != "" {
			res.Values[key] = value
		}
	}
	set(spec.MapsHost, res.Host)
	set(spec.MapsPort, strconv.Itoa(res.Port))
	set(spec.MapsUser, res.User)
	set(spec.MapsPassword, res.Password)
	set(spec.MapsDatabase, res.Database)
	set(spec.MapsURL, res.URL)
}

// buildURL arma la URL de conexión con net/url y no con Sprintf: una
// contraseña con un '@', un '/' o un ':' —posible si la cargó una persona
// con 'services connect'— partiría la URL en dos y el plugin terminaría
// conectándose a otro lugar, o a ninguno. Escapada, también vuelve exacta
// por StoredPassword.
func buildURL(kind string, res *Resolution) string {
	scheme := map[string]string{
		KindPostgres: "postgresql",
		KindMySQL:    "mysql",
		KindMariaDB:  "mysql",
	}[kind]
	if scheme != "" {
		u := &url.URL{
			Scheme: scheme,
			User:   url.UserPassword(res.User, res.Password),
			Host:   net.JoinHostPort(res.Host, strconv.Itoa(res.Port)),
			Path:   "/" + res.Database,
		}
		return u.String()
	}

	if kind != KindRedis {
		return ""
	}
	// Redis no tiene usuario en el esquema clásico: la contraseña va sola,
	// después de los dos puntos.
	u := &url.URL{Scheme: "redis", Host: net.JoinHostPort(res.Host, strconv.Itoa(res.Port))}
	if res.Password != "" {
		u.User = url.UserPassword("", res.Password)
	}
	return u.String()
}

// generatePassword arma una contraseña aleatoria de 24 bytes con
// crypto/rand — la misma fuente que ya usa internal/plugins para el
// external_ref y la master key. Base64 URL-safe para que no rompa una
// URL de conexión ni necesite escaping en el shell.
// GenerateAdminPassword es la contraseña del superusuario de un motor que
// Asterion levanta en un contenedor. Vive solo en memoria mientras dura el
// comando: se usa para crear la base y el usuario del plugin y se
// descarta. Lo único que queda persistido es la credencial del plugin, en
// su config cifrada.
func GenerateAdminPassword() (string, error) { return generatePassword() }

func generatePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("no pude generar una contraseña segura: %w", err)
	}
	return strings.TrimRight(base64.URLEncoding.EncodeToString(buf), "="), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
