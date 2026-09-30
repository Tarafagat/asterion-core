package pluginsvc

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Tarafagat/asterion-plugin-contract/apc"
)

// Levantar un motor en un contenedor es SIEMPRE un paso pedido
// explícitamente (el flag --create del CLI). Asterion nunca arranca un
// contenedor por su cuenta al detectar que falta algo: primero detecta y
// reusa lo que haya, y si no hay nada, lo dice y ofrece las opciones —
// arrancar un servicio que nadie pidió, con un puerto y un volumen
// nuevos en la máquina de alguien, es justo la clase de sorpresa que
// este proyecto evita.
//
// Docker y no un paquete del sistema porque es uniforme entre Linux y
// macOS, no necesita sudo, y se deshace con un 'docker rm' — un apt/dnf
// deja un servicio de sistema instalado que es mucho más difícil de
// revertir limpio. Mismo criterio que ya usan asterion-lab y el
// Dockerfile que genera 'plugin export'.

// images es la imagen por defecto de cada motor. Version del manifiesto
// pisa el tag: kind=postgres + version=16 -> postgres:16.
var images = map[string]string{
	KindPostgres: "postgres",
	KindMySQL:    "mysql",
	KindMariaDB:  "mariadb",
	KindRedis:    "redis",
}

// defaultTag se usa cuando el manifiesto no declara version. Fijo y
// explícito, nunca "latest": una imagen que cambia sola bajo los pies
// del plugin es una fuente de fallos irreproducibles.
var defaultTag = map[string]string{
	KindPostgres: "16",
	KindMySQL:    "8",
	KindMariaDB:  "11",
	KindRedis:    "7",
}

// Container describe el contenedor que Asterion gestiona para un
// servicio. El nombre lleva el plugin adentro para que dos plugins que
// necesiten un postgres cada uno no se pisen.
type Container struct {
	Name  string
	Image string
	Port  int
}

// ContainerName arma el nombre del contenedor de un servicio.
func ContainerName(pluginName, serviceName string) string {
	safe := func(s string) string {
		return strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
				return r
			case r >= 'A' && r <= 'Z':
				return r + 32
			default:
				return '-'
			}
		}, s)
	}
	return "asterion-" + safe(pluginName) + "-" + safe(serviceName)
}

// runningAsterionContainer devuelve el nombre del contenedor que Asterion
// tiene corriendo para este servicio Y que publica el puerto donde se
// detectó el motor, o "" si no hay ninguno (o si Docker no está: acá eso no
// es un error, solo significa "no hay contenedor").
//
// La comprobación del puerto no es un detalle: que exista un contenedor con
// ese nombre no dice que sea ÉL el que está escuchando donde miramos. Si el
// contenedor publica el 5434 y lo que responde en el 5432 es otro postgres
// cualquiera de la máquina, darlos por el mismo llevaría a crear la base y
// el usuario adentro del contenedor mientras se guarda en la config la
// dirección del otro servidor. El plugin apuntaría a una base que no tiene
// nada de lo que se creó.
func runningAsterionContainer(pluginName, serviceName string, port int) string {
	if _, err := exec.LookPath("docker"); err != nil {
		return ""
	}
	name := ContainerName(pluginName, serviceName)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// -q sobre contenedores CORRIENDO: uno detenido no sirve para nada acá.
	out, err := runCLI(ctx, "docker", nil, "ps", "-q", "-f", "name=^"+name+"$")
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	if hostPort, err := containerHostPort(ctx, name); err != nil || hostPort != port {
		return ""
	}
	return name
}

// DockerAvailable responde si hay un daemon de Docker con el que hablar.
func DockerAvailable() bool { return DockerUnavailableReason() == "" }

// DockerUnavailableReason devuelve "" si se puede usar Docker, y si no, por
// qué no — distinguiendo los dos casos, que piden arreglos distintos: que
// no esté el binario (hay que instalarlo) y que esté pero el daemon no
// conteste (está instalado y apagado, o el contexto activo apunta a un
// socket que no existe — típico con Docker Desktop cerrado, o con colima o
// podman, donde el contexto vive en el HOME del usuario). Decir "instalalo"
// cuando el docker está instalado manda a arreglar lo que no está roto.
func DockerUnavailableReason() string {
	if _, err := exec.LookPath("docker"); err != nil {
		return "no hay un 'docker' en el PATH de esta máquina"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput()
	if err == nil {
		return ""
	}
	return fmt.Sprintf("hay un 'docker' instalado pero su daemon no responde (%s) — arrancalo, o revisá que el contexto activo ('docker context ls') apunte a un socket que exista",
		firstLine(string(out), err))
}

// CreateContainer levanta el motor de un servicio en un contenedor
// gestionado por Asterion. adminPassword es la contraseña del superusuario
// del motor recién creado — la necesita Provision después para crear la
// base y el usuario del plugin.
//
// Si el contenedor ya existía (de una corrida anterior) se lo arranca en
// vez de crear otro: recrearlo borraría los datos que el plugin ya
// hubiera escrito.
func CreateContainer(ctx context.Context, spec apc.ServiceSpec, pluginName string, hostPort int, adminPassword string) (*Container, error) {
	if reason := DockerUnavailableReason(); reason != "" {
		return nil, fmt.Errorf("no puedo levantar un %s en un contenedor: %s", spec.Kind, reason)
	}

	image := images[spec.Kind]
	tag := firstNonEmpty(spec.Version, defaultTag[spec.Kind])
	if image == "" {
		return nil, fmt.Errorf("no sé qué imagen usar para kind %q", spec.Kind)
	}
	full := image + ":" + tag
	name := ContainerName(pluginName, spec.Name)

	// ¿Ya existe de antes? Arrancarlo, no recrearlo.
	if out, err := runCLI(ctx, "docker", nil, "ps", "-aq", "-f", "name=^"+name+"$"); err == nil && strings.TrimSpace(out) != "" {
		if out, err := runCLI(ctx, "docker", nil, "start", name); err != nil {
			return nil, fmt.Errorf("el contenedor %q ya existía pero no pude arrancarlo: %s", name, firstLine(out, err))
		}
		port, err := containerHostPort(ctx, name)
		if err != nil {
			return nil, err
		}
		return &Container{Name: name, Image: full, Port: port}, nil
	}

	args := []string{"run", "-d", "--name", name, "--restart", "unless-stopped",
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, defaultPort(spec.Kind)),
		"-v", name + "-data:" + dataDir(spec.Kind)}

	switch spec.Kind {
	case KindPostgres:
		args = append(args, "-e", "POSTGRES_PASSWORD="+adminPassword)
	case KindMySQL, KindMariaDB:
		args = append(args, "-e", "MYSQL_ROOT_PASSWORD="+adminPassword)
	}
	args = append(args, full)

	if out, err := runCLI(ctx, "docker", nil, args...); err != nil {
		return nil, fmt.Errorf("no pude levantar %s: %s", full, firstLine(out, err))
	}
	return &Container{Name: name, Image: full, Port: hostPort}, nil
}

// dataDir es dónde persiste cada motor adentro de su imagen oficial —
// sin el volumen, borrar el contenedor se llevaría los datos del plugin.
func dataDir(kind string) string {
	switch kind {
	case KindPostgres:
		return "/var/lib/postgresql/data"
	case KindMySQL, KindMariaDB:
		return "/var/lib/mysql"
	case KindRedis:
		return "/data"
	}
	return "/data"
}

func containerHostPort(ctx context.Context, name string) (int, error) {
	out, err := runCLI(ctx, "docker", nil, "inspect", "-f",
		`{{range $p, $conf := .NetworkSettings.Ports}}{{range $conf}}{{.HostPort}}{{end}}{{end}}`, name)
	if err != nil {
		return 0, fmt.Errorf("no pude averiguar en qué puerto quedó %q: %s", name, firstLine(out, err))
	}
	port, err := strconv.Atoi(strings.TrimSpace(strings.Fields(out)[0]))
	if err != nil {
		return 0, fmt.Errorf("no pude interpretar el puerto de %q: %q", name, strings.TrimSpace(out))
	}
	return port, nil
}

// WaitReady espera a que el motor recién arrancado esté de verdad listo
// para recibir queries. Un contenedor "running" no alcanza: postgres
// inicializa su cluster antes de escuchar, y mysql tarda todavía más.
//
// Y un TCP connect tampoco alcanza, lo cual no es obvio: el proxy de
// puertos de Docker acepta la conexión en el host DESDE QUE SE CREA EL
// MAPEO, aunque adentro no haya nada escuchando todavía — la conexión se
// acepta y se cierra en el acto. Esperar solo por TCP da un "ya está
// listo" falso, y el primer comando real falla con un "Server closed the
// connection" que parece un error del motor y no lo es.
//
// Así que se le pregunta al motor con su propio comando de salud, por
// 'docker exec' adentro del contenedor: no necesita ningún cliente
// instalado en el host (el psql/mysql del host puede no existir — ver
// Provision) y lo que contesta es el motor mismo, no el proxy.
func WaitReady(ctx context.Context, c *Container, spec apc.ServiceSpec, adminPassword string, timeout time.Duration) error {
	probe := healthProbe(spec.Kind)
	if len(probe) == 0 {
		return fmt.Errorf("no sé cómo preguntarle a un %s si ya está listo", spec.Kind)
	}

	// La clave del admin va por entorno del exec, no en los argumentos:
	// como argumento quedaría visible en el 'ps' de adentro del
	// contenedor. Mismo criterio que PGPASSWORD/MYSQL_PWD en engines.go.
	args := []string{"exec"}
	if adminPassword != "" {
		switch spec.Kind {
		case KindMySQL, KindMariaDB:
			args = append(args, "-e", "MYSQL_PWD="+adminPassword)
		}
	}
	args = append(append(args, c.Name), probe...)

	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		out, err := runCLI(ctx, "docker", nil, args...)
		if err == nil {
			return nil
		}
		// NOAUTH/ACCESS DENIED es el motor contestando: está listo.
		up := strings.ToUpper(out)
		if strings.Contains(up, "NOAUTH") || strings.Contains(up, "ACCESS DENIED") {
			return nil
		}
		last = firstLine(out, err)
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("el %s del contenedor %q no estuvo listo en %s (último intento: %s)",
		spec.Kind, c.Name, timeout, last)
}

// healthProbe es el comando de salud propio de cada motor, el mismo que
// usan los healthcheck de sus imágenes oficiales.
func healthProbe(kind string) []string {
	switch kind {
	case KindPostgres:
		return []string{"pg_isready", "-U", "postgres", "-q"}
	case KindMySQL, KindMariaDB:
		return []string{"mysqladmin", "ping", "-u", "root", "--silent"}
	case KindRedis:
		return []string{"redis-cli", "PING"}
	}
	return nil
}
