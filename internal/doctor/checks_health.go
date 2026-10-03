package doctor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"asterion-core/internal/plugins"
	"asterion-core/internal/pluginsvc"
)

const httpCheckTimeout = 4 * time.Second

// checkHealth mira si el plugin está de verdad corriendo y respondiendo —
// reconciliado contra el proceso real (mismo plugins.Status que usa
// 'asterion plugin status'), no solo lo que el registro dice que debería
// estar.
func checkHealth(ctx context.Context, installed plugins.Installed) []Check {
	var out []Check

	live, err := plugins.Status(installed.Name)
	if err != nil {
		return []Check{{Section: "Health", Name: "Backend", Severity: Fail, Detail: "no pude leer su estado: " + err.Error()}}
	}

	if live.Status != "running" {
		out = append(out, Check{Section: "Health", Name: "Backend", Severity: Fail,
			Detail: "no está corriendo — 'asterion plugin start " + installed.Name + "'"})
		return out // sin proceso, probar el health_path solo daría "conexión rechazada"
	}

	if live.Manifest.HealthPath == "" {
		out = append(out, Check{Section: "Health", Name: "Backend", Severity: Warn,
			Detail: fmt.Sprintf("corriendo (pid %d, puerto %d), pero no declara health_path — no se puede confirmar que responda de verdad", live.PID, live.Port)})
		return out
	}

	url := fmt.Sprintf("http://127.0.0.1:%d%s", live.Port, live.Manifest.HealthPath)
	reqCtx, cancel := context.WithTimeout(ctx, httpCheckTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	switch {
	case err != nil:
		out = append(out, Check{Section: "Health", Name: "Backend", Severity: Fail,
			Detail: fmt.Sprintf("proceso corriendo (pid %d) pero %s no respondió: %v", live.PID, url, err)})
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		resp.Body.Close()
		out = append(out, Check{Section: "Health", Name: "Backend", Severity: OK,
			Detail: fmt.Sprintf("%s respondió %d", url, resp.StatusCode)})
	default:
		resp.Body.Close()
		out = append(out, Check{Section: "Health", Name: "Backend", Severity: Fail,
			Detail: fmt.Sprintf("%s respondió %d", url, resp.StatusCode)})
	}

	if hasFrontend(installed.Dir) {
		// El frontend de un plugin no corre como proceso propio: se sirve a
		// través del reverse proxy de Asterion hacia el mismo backend, así
		// que su salud ES la del backend. Separarlo en su propio check
		// inventaría un dato que no existe.
		out = append(out, Check{Section: "Health", Name: "Frontend", Severity: out[len(out)-1].Severity,
			Detail: "servido vía el proxy de Asterion sobre el mismo backend — comparte su estado"})
	}

	return out
}

// checkServices corre pluginsvc.Detect sobre cada servicio declarado — la
// misma detección que usa 'asterion plugin services', acá integrada al
// reporte general. Cuando el servicio está listo, de paso le pregunta la
// versión real al motor con la credencial ya resuelta del plugin (no una
// de administrador: doctor no tiene, ni debe tener, esa).
func checkServices(ctx context.Context, installed plugins.Installed, config map[string]string) []Check {
	var out []Check
	for _, spec := range installed.Manifest.Services {
		st := pluginsvc.Detect(spec, installed.Name, config)
		name := strings.ToUpper(spec.Name[:1]) + spec.Name[1:]

		switch {
		case st.BadPort != "":
			out = append(out, Check{Section: "Health", Name: name, Severity: Fail, Detail: st.Detail})
			continue
		case st.Ready():
			detail := st.Detail
			if v, err := st.Version(ctx, config[spec.MapsUser], pluginsvc.StoredPassword(spec, config), config[spec.MapsDatabase]); err == nil && v != "" {
				detail = fmt.Sprintf("%s (versión del motor: %s)", detail, v)
			}
			out = append(out, Check{Section: "Health", Name: name, Severity: OK, Detail: detail})
		case st.Reachable:
			out = append(out, Check{Section: "Health", Name: name, Severity: Warn, Detail: st.Detail})
		default:
			out = append(out, Check{Section: "Health", Name: name, Severity: Fail, Detail: st.Detail})
		}
	}
	return out
}

// checkEnvironment compara el lenguaje que el plugin declaró contra lo que
// esta máquina realmente tiene instalado — un desajuste de versión mayor
// es la causa más común de "en mi máquina compilaba".
func checkEnvironment(ctx context.Context, installed plugins.Installed) []Check {
	lang := installed.Manifest.Language
	if lang == nil {
		return []Check{{Section: "Environment", Name: "Lenguaje", Severity: Warn,
			Detail: "el plugin no declara language.name — 'asterion plugin build' no va a saber cómo prepararlo"}}
	}

	var bin, versionFlag string
	switch lang.Name {
	case "go":
		bin, versionFlag = "go", "version"
	case "python":
		bin, versionFlag = pythonBin(), "--version"
	default:
		return []Check{{Section: "Environment", Name: "Lenguaje", Severity: Warn,
			Detail: fmt.Sprintf("declara language.name=%q — Asterion solo sabe compilar 'go' y 'python' hoy; preparalo a mano", lang.Name)}}
	}

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(reqCtx, bin, versionFlag).CombinedOutput()
	if err != nil {
		return []Check{{Section: "Environment", Name: strings.ToUpper(lang.Name[:1]) + lang.Name[1:], Severity: Fail,
			Detail: fmt.Sprintf("declara %s %s, pero no encontré %q en el PATH — no se puede compilar ni correr", lang.Name, lang.Version, bin)}}
	}

	installedVersion := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	sev := OK
	detail := installedVersion
	if lang.Version != "" && !strings.Contains(installedVersion, lang.Version) {
		sev = Warn
		detail = fmt.Sprintf("declara %s, esta máquina tiene %s — revisá si importa para este plugin", lang.Version, installedVersion)
	}
	return []Check{{Section: "Environment", Name: strings.ToUpper(lang.Name[:1]) + lang.Name[1:], Severity: sev, Detail: detail}}
}

func pythonBin() string {
	if _, err := exec.LookPath("python3"); err == nil {
		return "python3"
	}
	return "python"
}

func hasFrontend(dir string) bool {
	_, err := os.Stat(dir + "/frontend/package.json")
	return err == nil
}
