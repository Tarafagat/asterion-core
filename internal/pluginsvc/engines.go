package pluginsvc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Tarafagat/asterion-plugin-contract/apc"
)

// cmdTimeout acota cada invocación al CLI del motor — mismo criterio que
// internal/dbbackup: una conexión que queda colgada contra un host que
// acepta TCP pero no responde nunca dejaría el comando pendiendo para
// siempre.
const cmdTimeout = 20 * time.Second

// engineRunner es CÓMO se llega al cliente del motor, que son dos caminos
// y los dos hacen falta:
//
//   - el cliente instalado en el host (psql/mysql/redis-cli), para un motor
//     que ya existía en la máquina o en otro servidor;
//   - el cliente que ya viene adentro del contenedor, cuando Asterion
//     levantó ese contenedor él mismo. Esto no es un lujo: en macOS es
//     normal no tener psql instalado, y sin este camino un
//     'services up --create' levantaría el postgres y acto seguido diría
//     que no puede configurarlo — inútil.
//
// La diferencia no es solo el binario: adentro del contenedor la base
// escucha en su puerto interno, no en el que se publicó al host, así que
// el runner también decide a qué host:puerto conectarse.
type engineRunner struct {
	container string // vacío = cliente del host
	host      string
	port      int
}

func hostRunner(st Status) engineRunner {
	return engineRunner{host: st.Host, port: st.Port}
}

// containerRunner habla con el motor desde adentro de su propio
// contenedor, donde escucha en su puerto estándar.
func containerRunner(name, kind string) engineRunner {
	return engineRunner{container: name, host: "127.0.0.1", port: defaultPort(kind)}
}

// run ejecuta el cliente del motor. env son variables tipo
// PGPASSWORD/MYSQL_PWD: nunca argumentos, ni acá ni al cruzar a
// 'docker exec' (-e pasa el nombre, y el valor lo toma del entorno del
// proceso docker, así que tampoco aparece en la línea de comandos).
func (r engineRunner) run(ctx context.Context, bin string, env []string, args ...string) (string, error) {
	if r.container == "" {
		return runCLI(ctx, bin, env, args...)
	}
	full := []string{"exec"}
	for _, e := range env {
		if name, _, ok := strings.Cut(e, "="); ok {
			full = append(full, "-e", name)
		}
	}
	full = append(append(full, r.container, bin), args...)
	return runCLI(ctx, "docker", env, full...)
}

// pingRedis confirma que lo que escucha en ese puerto es de verdad un
// Redis y que se puede hablar con él — que acepte TCP no alcanza (podría
// ser cualquier cosa).
//
// La contraseña, si hay, viaja por REDISCLI_AUTH y no por '-a': redis-cli
// mismo advierte que un password en la línea de comandos es insegura, y
// queda visible en 'ps aux'. Mismo criterio que PGPASSWORD/MYSQL_PWD acá
// al lado.
func pingRedis(ctx context.Context, r engineRunner, spec apc.ServiceSpec, password string) error {
	host, port := r.host, r.port
	var env []string
	if password != "" {
		env = append(env, "REDISCLI_AUTH="+password)
	}
	out, err := r.run(ctx, "redis-cli", env, "-h", host, "-p", strconv.Itoa(port), "PING")

	// NOAUTH no es "no es un Redis": es la prueba de que SÍ lo es, y que
	// pide contraseña. Decir "¿es de verdad un Redis?" acá mandaría a
	// revisar el puerto cuando lo único que falta es la clave.
	if strings.Contains(strings.ToUpper(out), "NOAUTH") ||
		strings.Contains(strings.ToUpper(out), "WRONGPASS") {
		if password == "" {
			if spec.MapsPassword == "" {
				return fmt.Errorf("el redis de %s:%d pide contraseña, pero el servicio %q no declaró ningún maps_password en el plugin.yaml donde guardarla — agregalo con Contract.service(..., maps_password=\"<clave>\")",
					host, port, spec.Name)
			}
			return fmt.Errorf("el redis de %s:%d pide contraseña y no hay ninguna guardada en %s — cargala con 'asterion plugin services connect <plugin> %s'",
				host, port, spec.MapsPassword, spec.Name)
		}
		return fmt.Errorf("el redis de %s:%d rechazó la contraseña guardada — corregila con 'asterion plugin services connect'", host, port)
	}
	if err != nil {
		return fmt.Errorf("hay algo en %s:%d pero no respondió como Redis: %s", host, port, firstLine(out, err))
	}
	if !strings.Contains(strings.ToUpper(out), "PONG") {
		return fmt.Errorf("lo que escucha en %s:%d no respondió PONG a un PING (respondió %q) — ¿es de verdad un Redis?",
			host, port, strings.TrimSpace(out))
	}
	return nil
}

// provisionPostgres crea, si faltan, el rol y la base del plugin dentro
// de un Postgres que YA existe. Cada paso es idempotente: un rol o una
// base que ya estaban se dejan como están (no se rota la contraseña de
// un rol existente — ver Provision).
//
// Las credenciales de admin NUNCA van en la línea de comandos: viajan
// por PGPASSWORD en el entorno del proceso hijo. Un password como
// argumento queda visible en 'ps aux' para cualquier usuario de la
// máquina — mismo criterio que ya documenta 'asterion database backup'.
func provisionPostgres(ctx context.Context, r engineRunner, admin AdminCreds, opts ProvisionOptions, res *Resolution) error {
	adminUser := firstNonEmpty(admin.User, os.Getenv("PGUSER"), "postgres")
	env := []string{}
	if admin.Password != "" {
		env = append(env, "PGPASSWORD="+admin.Password)
	}

	base := []string{"-h", r.host, "-p", strconv.Itoa(r.port), "-U", adminUser, "-d", "postgres",
		"-v", "ON_ERROR_STOP=1", "-tAc"}

	// ¿Existe el rol?
	out, err := r.run(ctx, "psql", env, append(base, fmt.Sprintf("SELECT 1 FROM pg_roles WHERE rolname='%s'", sqlLit(res.User)))...)
	if err != nil {
		return fmt.Errorf("no pude consultar los roles de postgres como %q: %s", adminUser, firstLine(out, err))
	}
	exists := strings.TrimSpace(out) == "1"
	action, err := decidePassword(exists, opts, res)
	if err != nil {
		return err
	}
	switch {
	case !exists:
		if out, err := r.run(ctx, "psql", env, append(base,
			fmt.Sprintf("CREATE ROLE \"%s\" LOGIN PASSWORD '%s'", sqlIdent(res.User), sqlLit(res.Password)))...); err != nil {
			return fmt.Errorf("no pude crear el rol %q: %s", res.User, firstLine(out, err))
		}
	case opts.RotatePassword:
		if out, err := r.run(ctx, "psql", env, append(base,
			fmt.Sprintf("ALTER ROLE \"%s\" PASSWORD '%s'", sqlIdent(res.User), sqlLit(res.Password)))...); err != nil {
			return fmt.Errorf("no pude rotar la contraseña del rol %q: %s", res.User, firstLine(out, err))
		}
	}
	res.Actions = append(res.Actions, strings.Replace(action, "usuario", "rol", 1))

	// ¿Existe la base?
	out, err = r.run(ctx, "psql", env, append(base, fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname='%s'", sqlLit(res.Database)))...)
	if err != nil {
		return fmt.Errorf("no pude consultar las bases de postgres: %s", firstLine(out, err))
	}
	if strings.TrimSpace(out) == "1" {
		res.Actions = append(res.Actions, fmt.Sprintf("la base %q ya existía", res.Database))
	} else {
		// CREATE DATABASE no corre dentro de una transacción: va sola.
		if out, err := r.run(ctx, "psql", env, append(base,
			fmt.Sprintf("CREATE DATABASE \"%s\" OWNER \"%s\"", sqlIdent(res.Database), sqlIdent(res.User)))...); err != nil {
			return fmt.Errorf("no pude crear la base %q: %s", res.Database, firstLine(out, err))
		}
		res.Actions = append(res.Actions, fmt.Sprintf("base %q creada (owner: %s)", res.Database, res.User))
	}

	if out, err := r.run(ctx, "psql", env, append(base,
		fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE \"%s\" TO \"%s\"", sqlIdent(res.Database), sqlIdent(res.User)))...); err != nil {
		return fmt.Errorf("no pude dar permisos sobre %q a %q: %s", res.Database, res.User, firstLine(out, err))
	}
	res.Actions = append(res.Actions, fmt.Sprintf("permisos de %q otorgados sobre %q", res.User, res.Database))
	return nil
}

// provisionMySQL hace lo mismo para MySQL/MariaDB. La contraseña de
// admin va por MYSQL_PWD, no como argumento, por el mismo motivo que en
// Postgres.
//
// Acá NO se usa CREATE USER IF NOT EXISTS aunque sea más corto: cuando el
// usuario ya existe, esa forma no hace nada Y NO AVISA, así que el motor se
// queda con su contraseña vieja mientras nosotros escribiríamos en la
// config la que acabamos de generar. La credencial guardada no serviría, y
// el plugin fallaría a autenticar mucho después, sin pista de por qué. Hay
// que saber si existe para poder decidir — ver decidePassword.
func provisionMySQL(ctx context.Context, r engineRunner, admin AdminCreds, opts ProvisionOptions, res *Resolution) error {
	adminUser := firstNonEmpty(admin.User, "root")
	env := []string{}
	if admin.Password != "" {
		env = append(env, "MYSQL_PWD="+admin.Password)
	}
	base := []string{"-h", r.host, "-P", strconv.Itoa(r.port), "-u", adminUser, "--protocol=TCP", "-N", "-B", "-e"}

	run := func(sql string) (string, error) {
		out, err := r.run(ctx, "mysql", env, append(base, sql)...)
		if err != nil {
			return out, fmt.Errorf("no pude ejecutar %q como %q: %s", shorten(sql), adminUser, firstLine(out, err))
		}
		return out, nil
	}

	if _, err := run(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4", sqlIdent(res.Database))); err != nil {
		return err
	}
	res.Actions = append(res.Actions, fmt.Sprintf("base %q asegurada", res.Database))

	out, err := run(fmt.Sprintf("SELECT 1 FROM mysql.user WHERE user='%s'", sqlLit(res.User)))
	if err != nil {
		return err
	}
	exists := strings.TrimSpace(out) == "1"

	action, err := decidePassword(exists, opts, res)
	if err != nil {
		return err
	}
	switch {
	case !exists:
		if _, err := run(fmt.Sprintf("CREATE USER '%s'@'%%' IDENTIFIED BY '%s'", sqlLit(res.User), sqlLit(res.Password))); err != nil {
			return err
		}
	case opts.RotatePassword:
		if _, err := run(fmt.Sprintf("ALTER USER '%s'@'%%' IDENTIFIED BY '%s'", sqlLit(res.User), sqlLit(res.Password))); err != nil {
			return err
		}
	}
	res.Actions = append(res.Actions, action)

	if _, err := run(fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%'", sqlIdent(res.Database), sqlLit(res.User))); err != nil {
		return err
	}
	res.Actions = append(res.Actions, fmt.Sprintf("permisos de %q otorgados sobre %q", res.User, res.Database))
	if _, err := run("FLUSH PRIVILEGES"); err != nil {
		return err
	}
	return nil
}

// decidePassword resuelve el único punto realmente delicado de todo esto:
// qué contraseña va a quedar en la config cuando el usuario YA existe en el
// motor.
//
// Hay tres situaciones y ninguna admite adivinar:
//
//   - el usuario no existe: se crea con la contraseña generada. Caso fácil.
//   - existe y tenemos una guardada: se usa esa. Se verifica después
//     (verifyLogin), porque "la teníamos guardada" no prueba que siga siendo
//     la del motor.
//   - existe y NO tenemos ninguna: no hay forma de averiguar su contraseña
//     —un motor no la devuelve— así que se corta. Rotarla en silencio
//     dejaría afuera a cualquier otra cosa que estuviera usando ese mismo
//     usuario; escribir una inventada dejaría al plugin con una credencial
//     que no funciona. Se pide una decisión explícita (--rotate-password).
func decidePassword(exists bool, opts ProvisionOptions, res *Resolution) (string, error) {
	if !exists {
		return fmt.Sprintf("usuario %q creado", res.User), nil
	}
	if opts.RotatePassword {
		return fmt.Sprintf("usuario %q ya existía — contraseña ROTADA a una nueva (lo pediste con --rotate-password)", res.User), nil
	}
	if opts.ExistingPassword != "" {
		return fmt.Sprintf("usuario %q ya existía — se reusó la contraseña guardada, sin rotarla", res.User), nil
	}
	return "", fmt.Errorf("el usuario %q ya existe en el motor, pero no hay ninguna contraseña guardada para él en la config del plugin.\n"+
		"    No puedo averiguar la que tiene (ningún motor la devuelve) ni inventar una: quedaría guardada una credencial que no funciona.\n"+
		"    Elegí una: volvé a correr con --rotate-password para asignarle una nueva (ojo: si algo más usa ese mismo usuario, deja de andar),\n"+
		"    o cargá la que ya tenga a mano con 'asterion plugin services connect'", res.User)
}

// verifyLogin entra con la credencial que se va a guardar. Es el único
// chequeo que prueba de verdad que la config va a servir; sin él, un
// desajuste entre lo guardado y lo que tiene el motor recién aparece cuando
// el plugin arranca, como un error de autenticación sin contexto.
func verifyLogin(ctx context.Context, r engineRunner, kind string, res *Resolution) error {
	var out string
	var err error
	switch kind {
	case KindPostgres:
		out, err = r.run(ctx, "psql", []string{"PGPASSWORD=" + res.Password},
			"-h", r.host, "-p", strconv.Itoa(r.port), "-U", res.User, "-d", res.Database, "-tAc", "SELECT 1")
	case KindMySQL, KindMariaDB:
		out, err = r.run(ctx, "mysql", []string{"MYSQL_PWD=" + res.Password},
			"-h", r.host, "-P", strconv.Itoa(r.port), "-u", res.User, "--protocol=TCP", "-N", "-B", "-e", "SELECT 1", res.Database)
	default:
		return nil
	}
	if err != nil {
		return fmt.Errorf("la credencial que iba a guardar no funciona: %q no pudo entrar a %q (%s).\n"+
			"    No la guardo: dejaría al plugin con algo que no sirve. Si ese usuario ya existía con otra contraseña,\n"+
			"    corré con --rotate-password, o cargá la correcta con 'asterion plugin services connect'",
			res.User, res.Database, firstLine(out, err))
	}
	return nil
}

// runCLI corre el cliente del motor con timeout, sumando env al entorno
// heredado (para PGPASSWORD/MYSQL_PWD).
func runCLI(ctx context.Context, bin string, env []string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if runCtx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("%s no respondió en %s", bin, cmdTimeout)
	}
	return string(out), err
}

// sqlIdent/sqlLit escapan lo mínimo necesario para un identificador
// entre comillas y para un literal entre comillas simples. Los valores
// que pasan por acá salen del plugin.yaml (nombre de base/usuario) y de
// crypto/rand (la contraseña), no de entrada de red — pero escaparlos
// igual es lo correcto: un plugin de un tercero escribe ese manifiesto.
func sqlIdent(s string) string { return strings.ReplaceAll(s, `"`, `""`) }
func sqlLit(s string) string {
	return strings.NewReplacer(`'`, `''`, `\`, `\\`).Replace(s)
}

func firstLine(out string, err error) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return err.Error()
	}
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	return out
}

func shorten(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
