package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/Tarafagat/asterion-plugin-contract/apc"

	"asterion-core/internal/plugins"
	"asterion-core/internal/pluginsvc"
)

// mcpPluginName es el "dueño" que ven los contenedores que esta sesión de
// MCP crea (asterion-mcp-postgres, asterion-mcp-redis...) — separados por
// nombre de los que pertenecen a un plugin instalado, para que
// 'docker ps' y 'asterion plugin services' de un plugin real nunca se
// confundan con un entorno ad hoc que pidió un agente.
const mcpPluginName = "mcp"

var supportedKinds = map[string]bool{
	pluginsvc.KindPostgres: true, pluginsvc.KindMySQL: true,
	pluginsvc.KindMariaDB: true, pluginsvc.KindRedis: true,
}

func serviceKindSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"required":    []string{"kind"},
		"properties": map[string]any{
			"kind": map[string]any{
				"type":        "string",
				"enum":        []string{"postgres", "mysql", "mariadb", "redis"},
				"description": "El motor que se necesita.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Nombre lógico para distinguir varias instancias del mismo motor (default: el nombre del motor).",
			},
		},
	}
}

// createOwnContainer levanta un contenedor de Asterion para spec en
// hostPort y espera a que el motor esté listo, devolviendo el Status y
// las credenciales de administrador para usarlo recién creado. Separado
// del handler porque toolCreateEnvironment lo necesita en dos momentos
// distintos: cuando no hay nada reachable, y como fallback cuando lo que
// SÍ está reachable resultó no servir (ver el comentario ahí).
func createOwnContainer(ctx context.Context, spec apc.ServiceSpec, hostPort int) (pluginsvc.Status, pluginsvc.AdminCreds, error) {
	if reason := pluginsvc.DockerUnavailableReason(); reason != "" {
		return pluginsvc.Status{}, pluginsvc.AdminCreds{}, fmt.Errorf("%s", reason)
	}
	pw, err := pluginsvc.GenerateAdminPassword()
	if err != nil {
		return pluginsvc.Status{}, pluginsvc.AdminCreds{}, err
	}
	c, err := pluginsvc.CreateContainer(ctx, spec, mcpPluginName, hostPort, pw)
	if err != nil {
		return pluginsvc.Status{}, pluginsvc.AdminCreds{}, err
	}

	admin := pluginsvc.AdminCreds{Password: pw, User: "root"}
	if spec.Kind == pluginsvc.KindPostgres {
		admin.User = "postgres"
	}

	waitCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := pluginsvc.WaitReady(waitCtx, c, spec, pw, 90*time.Second); err != nil {
		return pluginsvc.Status{}, pluginsvc.AdminCreds{}, err
	}

	st := pluginsvc.Status{Service: spec, Host: "127.0.0.1", Port: c.Port, Container: c.Name, Reachable: true}
	return st, admin, nil
}

func adHocSpec(kind, name string) (apc.ServiceSpec, error) {
	if !supportedKinds[kind] {
		return apc.ServiceSpec{}, fmt.Errorf("kind %q no soportado — tiene que ser postgres, mysql, mariadb o redis", kind)
	}
	if name == "" {
		name = kind
	}
	return apc.ServiceSpec{Name: name, Kind: kind}, nil
}

// toolRunService detecta si YA hay un motor de ese tipo alcanzable —
// nunca crea nada. Es el equivalente de 'asterion plugin services' para
// un agente que solo quiere saber "¿tengo un Postgres a mano ahora
// mismo?" antes de decidir si hace falta levantar uno.
func toolRunService() Tool {
	return Tool{
		Name:        "run_service",
		Description: "Detecta si ya hay un motor (postgres/mysql/mariadb/redis) alcanzable en esta máquina — SOLO detecta, nunca crea nada. Si no hay nada, usá create_environment.",
		InputSchema: serviceKindSchema(),
		Handler: func(ctx context.Context, args map[string]any) Result {
			spec, err := adHocSpec(argString(args, "kind", ""), argString(args, "name", ""))
			if err != nil {
				return fail(err.Error())
			}
			// Detect() usa "Configured" para "¿la config del plugin ya
			// apunta ahí?" — acá no hay ningún plugin ni maps_*, así que
			// esa parte del Detail no dice nada útil; solo importa si
			// responde o no.
			st := pluginsvc.Detect(spec, mcpPluginName, map[string]string{})
			if !st.Reachable {
				return ok(fmt.Sprintf("No hay ningún %s alcanzable en %s:%d ahora mismo.\nUsá create_environment si necesitás que se levante uno.",
					spec.Kind, st.Host, st.Port))
			}
			return ok(fmt.Sprintf("Hay un %s respondiendo en %s:%d.\nPara usarlo con credenciales propias (no de administrador), llamá a create_environment — detecta esto mismo y, si hace falta, asegura una base y un usuario dedicados ahí dentro.",
				spec.Kind, st.Host, st.Port))
		},
	}
}

// toolCreateEnvironment es la única tool que puede terminar levantando un
// contenedor — y aun así, primero detecta y reusa lo que ya haya
// corriendo (mismo orden que 'plugin services up'). Un agente que la
// llama YA hizo el pedido explícito que este proyecto exige en cualquier
// otro lado antes de crear infraestructura; no hace falta un flag
// --create acá porque llamar a esta tool puntual ES el pedido explícito.
//
// Las credenciales que devuelve son de un usuario de aplicación recién
// creado (o reusado, si ya existía) — nunca las de administrador del
// motor, y nunca acceso a un shell del host: el agente que pidió esto
// recibe host/puerto/usuario/contraseña de SU base, no las llaves de la
// máquina que la corre.
func toolCreateEnvironment() Tool {
	return Tool{
		Name:        "create_environment",
		Description: "Asegura un motor (postgres/mysql/mariadb/redis) USABLE YA: reusa uno que ya esté corriendo, o si no hay ninguno, levanta uno en un contenedor propio y crea un usuario de aplicación dedicado. Devuelve host/puerto/usuario/contraseña de ESE usuario — nunca credenciales de administrador ni acceso al host.",
		InputSchema: serviceKindSchema(),
		Handler: func(ctx context.Context, args map[string]any) Result {
			spec, err := adHocSpec(argString(args, "kind", ""), argString(args, "name", ""))
			if err != nil {
				return fail(err.Error())
			}

			st := pluginsvc.Detect(spec, mcpPluginName, map[string]string{})
			var admin pluginsvc.AdminCreds
			var ownContainer bool

			if !st.Reachable {
				newSt, newAdmin, err := createOwnContainer(ctx, spec, st.Port)
				if err != nil {
					return fail(fmt.Sprintf("no hay ningún %s corriendo y no puedo levantar un contenedor: %v", spec.Kind, err))
				}
				st, admin, ownContainer = newSt, newAdmin, true
			}

			// Redis no tiene usuario propio que crear — Provision lo nota
			// y solo confirma que responde.
			res, err := pluginsvc.Provision(ctx, spec, st, pluginsvc.ProvisionOptions{Admin: admin})
			if err != nil {
				if ownContainer {
					// Esto es nuestro, recién creado, y aun así falló — no
					// hay un segundo intento razonable, es un error real.
					return fail(err.Error())
				}
				// Había algo respondiendo en el puerto estándar, pero no se
				// pudo USAR (pide una contraseña que no tenemos, falta el
				// cliente para hablarle, etc.) — eso no es lo mismo que
				// "no hay nada". En vez de devolver un error que menciona
				// un 'plugin.yaml' que acá ni existe, se cae a levantar un
				// contenedor propio, en un puerto libre (el estándar ya
				// está tomado por lo que no se pudo usar).
				freePort, perr := plugins.FreePort()
				if perr != nil {
					return fail(fmt.Sprintf("hay un %s en %s:%d pero no lo pude usar (%v), y no pude reservar un puerto libre para uno propio: %v",
						spec.Kind, st.Host, st.Port, err, perr))
				}
				newSt, newAdmin, cerr := createOwnContainer(ctx, spec, freePort)
				if cerr != nil {
					return fail(fmt.Sprintf("hay un %s en %s:%d pero no lo pude usar (%v), y no pude levantar uno propio: %v",
						spec.Kind, st.Host, st.Port, err, cerr))
				}
				res, err = pluginsvc.Provision(ctx, spec, newSt, pluginsvc.ProvisionOptions{Admin: newAdmin})
				if err != nil {
					return fail(err.Error())
				}
			}

			if spec.Kind == pluginsvc.KindRedis {
				return ok(fmt.Sprintf("Redis listo en %s:%d.\nurl: %s", res.Host, res.Port, res.URL))
			}
			return ok(fmt.Sprintf(
				"%s listo en %s:%d.\nusuario: %s\ncontraseña: %s\nbase: %s\nurl: %s\n\n(usuario de aplicación propio — no es el administrador del motor)",
				spec.Kind, res.Host, res.Port, res.User, res.Password, res.Database, res.URL))
		},
	}
}

// toolRequestCapability es un único punto de entrada para quien prefiere
// no acordarse de los nombres de cada tool: "capability" elige cuál de
// las acciones reales de abajo correr. No agrega una capa de permisos
// propia — no existe un motor de políticas detrás, y decir que lo hay
// sería prometer una garantía de seguridad que no está implementada. Lo
// que SÍ es real: cada capability dispara exactamente la misma función
// que su tool dedicada, con las mismas reglas (detectar antes de crear,
// nunca credenciales de administrador de vuelta).
func toolRequestCapability(tools map[string]Tool) Tool {
	return Tool{
		Name: "request_capability",
		Description: "Un único punto de entrada a las demás herramientas, por nombre de capability (\"database\", \"cache\", \"logs\", \"tests\", \"deploy\") en vez de tener que saber el nombre de cada tool. No es una capa de permisos: despacha a la tool real correspondiente, con las mismas reglas que tiene esa tool llamada directo.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"capability"},
			"properties": map[string]any{
				"capability": map[string]any{
					"type": "string", "enum": []string{"database", "cache", "logs", "tests", "deploy"},
				},
				"mode": map[string]any{
					"type": "string", "enum": []string{"detect", "create"},
					"description": "Para \"database\"/\"cache\": \"detect\" (default) solo mira si ya hay uno; \"create\" asegura uno usable.",
				},
				"name":   map[string]any{"type": "string", "description": "El plugin, para \"logs\"/\"tests\"/\"deploy\"."},
				"target": map[string]any{"type": "string", "description": "Para \"deploy\": el destino (hoy solo \"local\")."},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) Result {
			cap := argString(args, "capability", "")
			var delegate string
			switch cap {
			case "database":
				if argString(args, "mode", "detect") == "create" {
					delegate = "create_environment"
				} else {
					delegate = "run_service"
				}
				if args["kind"] == nil {
					args["kind"] = "postgres"
				}
			case "cache":
				if argString(args, "mode", "detect") == "create" {
					delegate = "create_environment"
				} else {
					delegate = "run_service"
				}
				args["kind"] = "redis"
			case "logs":
				delegate = "read_logs"
			case "tests":
				delegate = "run_tests"
			case "deploy":
				delegate = "deploy_preview"
			default:
				return fail(fmt.Sprintf("capability %q no reconocida — usá database, cache, logs, tests o deploy", cap))
			}
			t, ok := tools[delegate]
			if !ok {
				return fail("la tool " + delegate + " no está registrada")
			}
			return t.Handler(ctx, args)
		},
	}
}
