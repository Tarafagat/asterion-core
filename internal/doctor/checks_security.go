package doctor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"asterion-core/internal/plugins"
	"asterion-core/internal/pluginsvc"
)

// dotenvLikeNames son los nombres de archivo que casi siempre llevan
// secretos reales — la lista que importa para "¿un secreto quedó
// commiteado?". No es exhaustiva (nadie puede serlo: un secreto puede
// estar en cualquier archivo), es la señal barata y de alta confianza:
// si UNO de estos está trackeado por git, vale la pena mirar.
var dotenvLikeNames = []string{".env", ".env.local", ".env.production", ".env.development", ".env.staging"}

// checkSecurity mira lo que de verdad se puede comprobar sin fingir que
// Asterion aplica algo que no aplica. Dos tipos de hallazgo conviven acá
// a propósito, y el Detail siempre dice cuál es cuál:
//
//   - lo que Asterion mismo FUERZA (un contenedor que levantó él nunca es
//     --privileged, y publica solo en 127.0.0.1) — eso sí puede decir "✓".
//   - lo que el plugin.yaml DECLARA (permissions.filesystem/network) — eso
//     es intención del autor del plugin, no algo que nada esté aplicando,
//     y el check lo rotula "declarado" sin excepción.
func checkSecurity(ctx context.Context, installed plugins.Installed) []Check {
	var out []Check
	out = append(out, checkCommittedSecrets(installed)...)
	out = append(out, checkDeclaredFilesystem(installed)...)
	out = append(out, checkContainerSafety(ctx, installed)...)
	return out
}

// checkCommittedSecrets busca archivos tipo .env que estén TRACKEADOS por
// git en el repo del plugin — el riesgo concreto y nombrado en el pedido
// original: un secreto que quedó en el historial y, si el repo tiene
// remoto, ya viajó ahí. Que el archivo exista en el disco no es el
// problema (un .env sin trackear, gitignorado, es exactamente lo
// correcto) — el problema es que git lo esté seseando.
func checkCommittedSecrets(installed plugins.Installed) []Check {
	if _, err := exec.LookPath("git"); err != nil {
		return []Check{{Section: "Security", Name: "Secretos", Severity: NA, Detail: "no hay 'git' en el PATH para poder revisar"}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", installed.Dir, "ls-files").CombinedOutput()
	if err != nil {
		// No es un repo git (plugin --link sin inicializar, por ejemplo) —
		// no hay nada que revisar, y no es una falla del plugin.
		return []Check{{Section: "Security", Name: "Secretos", Severity: NA, Detail: "esta carpeta no es un repo git — nada que revisar"}}
	}

	tracked := strings.Split(strings.TrimSpace(string(out)), "\n")
	var found []string
	for _, f := range tracked {
		base := f
		if i := strings.LastIndexByte(f, '/'); i >= 0 {
			base = f[i+1:]
		}
		for _, bad := range dotenvLikeNames {
			if base == bad {
				found = append(found, f)
			}
		}
	}

	if len(found) == 0 {
		return []Check{{Section: "Security", Name: "Secretos", Severity: OK, Detail: "ningún .env trackeado por git"}}
	}
	return []Check{{Section: "Security", Name: "Secretos", Severity: Fail,
		Detail: fmt.Sprintf("commiteado en el repo: %s — sacalo del tracking ('git rm --cached %s') y rotá lo que tuviera adentro, ya viajó al historial", strings.Join(found, ", "), found[0])}}
}

// checkDeclaredFilesystem y la red son DECLARACIONES del manifiesto, nunca
// una regla que algo esté aplicando — ver el comentario de paquete.
func checkDeclaredFilesystem(installed plugins.Installed) []Check {
	perms := installed.Manifest.Permissions
	if perms == nil {
		return []Check{
			{Section: "Security", Name: "Filesystem", Severity: Warn, Detail: "el plugin no declara permissions — no se sabe qué necesita tocar"},
			{Section: "Networking", Name: "Red", Severity: Warn, Detail: "el plugin no declara permissions.network — no se sabe a qué necesita salir"},
		}
	}

	var out []Check

	switch {
	case len(perms.Filesystem) == 0:
		out = append(out, Check{Section: "Security", Name: "Filesystem", Severity: Warn, Detail: "declara permissions, pero sin filesystem — no dice qué rutas necesita"})
	case declaresUnrestricted(perms.Filesystem):
		out = append(out, Check{Section: "Security", Name: "Filesystem", Severity: Warn,
			Detail: fmt.Sprintf("declarado sin restricción (%s) — nada lo está aplicando, es la intención que el autor del plugin escribió", strings.Join(perms.Filesystem, ", "))})
	default:
		out = append(out, Check{Section: "Security", Name: "Filesystem", Severity: OK,
			Detail: fmt.Sprintf("declarado acotado a: %s", strings.Join(perms.Filesystem, ", "))})
	}

	switch {
	case len(perms.Network) == 0:
		out = append(out, Check{Section: "Networking", Name: "Red", Severity: OK, Detail: "no declara ningún host de salida — se asume que no necesita red"})
	case declaresUnrestricted(perms.Network):
		out = append(out, Check{Section: "Networking", Name: "Red", Severity: Warn,
			Detail: "declarado sin restricción — nada lo está aplicando, es la intención que el autor del plugin escribió"})
	default:
		out = append(out, Check{Section: "Networking", Name: "Red", Severity: OK,
			Detail: fmt.Sprintf("declarado solo hacia: %s", strings.Join(perms.Network, ", "))})
	}

	return out
}

func declaresUnrestricted(paths []string) bool {
	for _, p := range paths {
		switch strings.TrimSpace(p) {
		case "/", "*", "0.0.0.0", "0.0.0.0/0", "any", "all", "~":
			return true
		}
	}
	return false
}

// checkContainerSafety es, al revés de los dos checks de arriba, algo que
// Asterion SÍ puede afirmar con un "✓": cada contenedor que crea
// internal/pluginsvc/container.go lo hace sin --privileged y publicando
// solo en 127.0.0.1 (ver CreateContainer) — se verifica en vivo contra
// 'docker inspect' en vez de solo confiar en que el código no cambió,
// por si alguien recreó el contenedor a mano con otros flags.
func checkContainerSafety(ctx context.Context, installed plugins.Installed) []Check {
	if len(installed.Manifest.Services) == 0 {
		return nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return []Check{{Section: "Security", Name: "Contenedores", Severity: NA, Detail: "no hay 'docker' en el PATH para poder revisar"}}
	}

	var names []string
	for _, spec := range installed.Manifest.Services {
		names = append(names, pluginsvc.ContainerName(installed.Name, spec.Name))
	}

	runCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var checked int
	for _, name := range names {
		out, err := exec.CommandContext(runCtx, "docker", "inspect", "-f",
			"{{.HostConfig.Privileged}}|{{range $p,$c := .NetworkSettings.Ports}}{{range $c}}{{.HostIp}}{{end}}{{end}}", name).CombinedOutput()
		if err != nil {
			continue // ese servicio no corre en un contenedor nuestro — nada que revisar
		}
		checked++
		fields := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
		privileged := len(fields) > 0 && fields[0] == "true"
		hostIP := ""
		if len(fields) > 1 {
			hostIP = fields[1]
		}
		if privileged {
			return []Check{{Section: "Security", Name: "Contenedores", Severity: Fail,
				Detail: fmt.Sprintf("%q corre --privileged — Asterion nunca crea uno así; si lo tocaron a mano, recreálo con 'plugin services up --create'", name)}}
		}
		if hostIP != "" && hostIP != "127.0.0.1" {
			return []Check{{Section: "Security", Name: "Contenedores", Severity: Fail,
				Detail: fmt.Sprintf("%q publica su puerto en %s, no en 127.0.0.1 — quedó expuesto a la red, no solo a esta máquina", name, hostIP)}}
		}
	}

	if checked == 0 {
		return nil
	}
	return []Check{{Section: "Security", Name: "Contenedores", Severity: OK,
		Detail: "sin --privileged, publicando solo en 127.0.0.1 — verificado contra 'docker inspect', no asumido"}}
}

