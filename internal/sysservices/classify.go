package sysservices

import "strings"

// Category es una clasificación best-effort de una unidad, a partir de su
// nombre únicamente — nunca de la descripción, que varía demasiado entre
// distros/versiones de systemd como para confiar en ella, y nunca sondeando
// puertos/PID real (eso sería mucho más pesado de hacer para un scan de
// 100-300 unidades, y es una extensión aparte si algún día hace falta más
// precisión que esto). Mismo criterio de honestidad que el resto de
// Asterion ("nunca se simula una capacidad que no se implementó" aplicado
// acá a "nunca se inventa una categoría que no se pudo confirmar de
// verdad") — lo que no matchea ningún patrón conocido cae en
// CategoryOther, no se lo fuerza a system/database/api por descarte.
type Category string

const (
	// CategorySystem: infraestructura del propio sistema operativo — red,
	// ssh, tiempo, logs, seguridad, hardware, paquetes. Lo que ya está
	// protegido contra control remoto (ver protectedExact) es un
	// subconjunto de esto, pero Category se calcula para TODAS las
	// unidades, protegidas o no.
	CategorySystem Category = "system"
	// CategoryDatabase: motores de base de datos conocidos — separada de
	// "api" porque Asterion ya trata backups de base de datos como su
	// propia feature (ver internal/dbbackup), y agruparla junto con
	// servidores web genéricos perdería esa distinción de un vistazo.
	CategoryDatabase Category = "database"
	// CategoryAPI: proxies/servidores web conocidos, demonios que de
	// verdad exponen una API propia (Docker Engine API por socket unix), y
	// servicios de aplicación con nombre propio reconocidos por sufijo
	// (ver apiSuffixes) — no hay forma de listar cada nombre que alguien
	// le ponga a su propio backend.
	CategoryAPI Category = "api"
	// CategoryOther: todo lo que no matcheó nada de lo anterior — cron
	// jobs, timers, VPN clients, agentes de monitoreo, mounts, etc. No es
	// "no importa", es "no se pudo clasificar con confianza".
	CategoryOther Category = "other"
)

// systemUnits: lista curada y chica a propósito (mismo criterio que
// protectedExact en sysservices.go) — no un intento de cubrir cada
// distro/init system posible, solo lo suficientemente común como para que
// la mayoría de una instancia recién provisionada quede bien agrupada.
var systemUnits = map[string]bool{
	"ssh": true, "sshd": true,
	"networking": true, "networkmanager": true, "wpa_supplicant": true,
	"cron": true, "crond": true, "atd": true,
	"rsyslog": true, "syslog": true,
	"dbus": true, "dbus-broker": true,
	"udisks2": true, "polkit": true, "accounts-daemon": true,
	"ufw": true, "firewalld": true, "iptables": true, "nftables": true, "apparmor": true, "auditd": true,
	"snapd": true, "unattended-upgrades": true, "packagekit": true,
	"chronyd": true, "ntpd": true,
	"getty": true, "serial-getty": true,
	"lvm2-monitor": true, "multipathd": true,
	"cups": true, "cupsd": true,
}

// databaseUnits: motores de base de datos conocidos por su unidad
// systemd habitual (no por el nombre del paquete, que a veces difiere).
var databaseUnits = map[string]bool{
	"mysql": true, "mysqld": true, "mariadb": true,
	"postgresql": true,
	"redis":      true, "redis-server": true,
	"mongod": true, "mongodb": true,
	"memcached": true, "elasticsearch": true, "cassandra": true,
}

// apiUnits: proxies/servidores web conocidos + demonios que de verdad
// exponen una API propia.
var apiUnits = map[string]bool{
	"nginx": true, "apache2": true, "httpd": true, "caddy": true, "traefik": true, "haproxy": true,
	"docker": true, "containerd": true,
}

// apiSuffixes: heurística de nombre para servicios de aplicación propios
// (ej. "asterion-cloud-backend", "mi-app-api") — sufijo, no nombre exacto,
// porque no hay forma de anticipar cómo alguien va a llamar a su propio
// backend.
var apiSuffixes = []string{"-backend", "-api", "-app", "-webapp"}

// baseUnitName saca ".service" y, si es una unidad templada (ej.
// "postgresql@14-main.service", "getty@tty1.service"), la parte después de
// "@" — para que la clasificación mire "postgresql"/"getty" (el tipo de
// unidad), no "14-main"/"tty1" (la instancia puntual).
func baseUnitName(name string) string {
	name = strings.TrimSuffix(name, ".service")
	if i := strings.Index(name, "@"); i >= 0 {
		name = name[:i]
	}
	return strings.ToLower(name)
}

// Classify decide la categoría de una unidad a partir de su nombre. El
// orden de chequeo importa: las listas curadas (más específicas) se
// prueban antes que la heurística de sufijo (más amplia), para que un
// nombre que casualmente calce con un sufijo de "api" pero ya esté en una
// lista más específica no se reclasifique.
//
// Si el match exacto falla Y el nombre NO tiene forma de unidad systemd
// (no termina en ".service"), se prueba un segundo paso por substring
// contra las mismas listas curadas (ver matchBySubstring) — agregado para
// que nombres de launchd/Windows (que no tienen la forma limpia
// "nombre.service" de systemd — ej. "com.vix.cron", "postgresql-x64-14",
// "MySQL80") también clasifiquen bien sin mantener una lista curada nueva
// por plataforma. El chequeo de sufijo ".service" (no un build tag) es lo
// que decide si el paso por substring corre — determinístico a partir del
// nombre, no de en qué SO se compiló — y es lo que mantiene a Linux
// exactamente como estaba: un nombre systemd real siempre termina en
// ".service", así que ahí el match exacto sigue siendo la única palabra
// final, sin el riesgo de falso positivo que sí vale la pena aceptar para
// plataformas sin esa convención (ver test que prueba justo este caso:
// "random-cron-job.service" debe seguir siendo CategoryOther).
func Classify(unitName string) Category {
	base := baseUnitName(unitName)
	switch {
	case systemUnits[base]:
		return CategorySystem
	case databaseUnits[base]:
		return CategoryDatabase
	case apiUnits[base]:
		return CategoryAPI
	}
	for _, suffix := range apiSuffixes {
		if strings.HasSuffix(base, suffix) {
			return CategoryAPI
		}
	}
	if strings.HasSuffix(unitName, ".service") {
		return CategoryOther
	}
	switch {
	case matchBySubstring(base, systemUnits):
		return CategorySystem
	case matchBySubstring(base, databaseUnits):
		return CategoryDatabase
	case matchBySubstring(base, apiUnits):
		return CategoryAPI
	}
	return CategoryOther
}

// matchBySubstring confirma si el nombre CONTIENE (no es igual a) alguna
// de las claves de la lista curada — best-effort a propósito, mismo
// criterio de "nunca se inventa una categoría que no se pudo confirmar de
// verdad" que el resto de este archivo: un falso positivo puntual acá es
// preferible a mantener una lista curada separada por plataforma.
func matchBySubstring(base string, known map[string]bool) bool {
	for key := range known {
		if strings.Contains(base, key) {
			return true
		}
	}
	return false
}
