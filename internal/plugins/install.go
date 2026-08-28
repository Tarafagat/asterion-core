package plugins

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	apc "github.com/Tarafagat/asterion-plugin-contract/apc"
)

var repoNamePattern = regexp.MustCompile(`([a-zA-Z0-9_-]+?)(\.git)?/?$`)

// deriveName saca un nombre de plugin de la URL del repo
// (.../asterion-plugin-sii -> "sii"; .../mi-plugin -> "mi-plugin"). El
// prefijo "asterion-plugin-" es una convención sugerida, no obligatoria —
// si no está, se usa el nombre del repo tal cual.
func deriveName(repoURL string) string {
	match := repoNamePattern.FindStringSubmatch(repoURL)
	base := repoURL
	if len(match) > 1 {
		base = match[1]
	}
	base = strings.TrimPrefix(base, "asterion-plugin-")
	return strings.ToLower(base)
}

// Install clona repoURL, valida su plugin.yaml y lo registra como plugin
// instalado. No ejecuta nada del repo salvo leer ese único archivo
// declarativo — instalar nunca corre código de un tercero.
//
// repoURL acepta cualquier cosa que `git clone` acepte, incluidas URLs
// SSH/HTTPS de repos privados: si el git del usuario ya tiene credenciales
// configuradas para ese repo (agente SSH, credential helper), la
// instalación funciona igual que con uno público — Asterion no necesita
// (ni debe) manejar esas credenciales por su cuenta. Así "plugins
// privados" no es una funcionalidad aparte, es gratis por construcción.
//
// link=true salta git por completo: repoURL se trata como una carpeta
// local ya existente (ver InstallLinked) — para el caso más simple
// todavía de "privado": una carpeta en el propio disco, sin repo alguno.
func Install(repoURL, nameOverride string, link bool) (Installed, error) {
	if link {
		return InstallLinked(repoURL, nameOverride)
	}
	if repoURL == "" {
		return Installed{}, fmt.Errorf("falta la URL (o ruta) del repo del plugin")
	}
	name := nameOverride
	if name == "" {
		name = deriveName(repoURL)
	}
	if !apc.IsValidName(name) {
		return Installed{}, fmt.Errorf("no pude derivar un nombre de plugin válido de %q — pasá uno explícito con --name", repoURL)
	}

	if _, err := Get(name); err == nil {
		return Installed{}, fmt.Errorf("ya hay un plugin instalado llamado %q — 'asterion plugin remove %s' primero si querés reinstalarlo", name, name)
	}

	dir, err := ReposDir(name)
	if err != nil {
		return Installed{}, err
	}
	if _, err := os.Stat(dir); err == nil {
		return Installed{}, fmt.Errorf("%s ya existe en disco pero no está registrado — borralo a mano o elegí otro --name", dir)
	}

	if _, err := exec.LookPath("git"); err != nil {
		return Installed{}, fmt.Errorf("necesito 'git' en el PATH para instalar plugins (clona el repo del plugin) — instalalo y reintentá")
	}

	cmd := exec.Command("git", "clone", "--depth", "1", repoURL, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		return Installed{}, fmt.Errorf("git clone falló: %s", strings.TrimSpace(string(out)))
	}

	manifest, err := LoadManifest(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return Installed{}, err
	}
	if manifest.Name != name {
		// El nombre del manifiesto manda sobre el derivado de la URL, pero
		// el directorio ya se clonó con el derivado — para no complicar el
		// layout en disco, se exige que coincidan.
		_ = os.RemoveAll(dir)
		return Installed{}, fmt.Errorf(
			"plugin.yaml declara name=%q pero se instaló como %q — usá --name %s para que coincidan",
			manifest.Name, name, manifest.Name,
		)
	}

	installed := Installed{
		ExternalRef: NewExternalRef(),
		Name:        name,
		Dir:         dir,
		Manifest:    manifest,
		Port:        manifest.Port,
		Status:      "stopped",
		InstalledAt: time.Now(),
	}
	if err := Save(installed); err != nil {
		_ = os.RemoveAll(dir)
		return Installed{}, err
	}
	return installed, nil
}

// InstallLinked registra una carpeta local ya existente como plugin
// instalado, SIN copiarla ni clonarla a ningún lado — Dir apunta directo
// a dirPath. Pensado para desarrollar un plugin privado (o simplemente
// probarlo) sin publicarlo a ningún repo git: se edita el código ahí
// mismo, y 'asterion plugin start' ya lo levanta desde esa carpeta tal
// como está en cada momento (después de compilarlo, igual que cualquier
// plugin — Asterion nunca compila nada por su cuenta).
//
// Nunca se ejecuta nada de dirPath salvo leer su plugin.yaml, mismo
// criterio que Install.
func InstallLinked(dirPath, nameOverride string) (Installed, error) {
	if dirPath == "" {
		return Installed{}, fmt.Errorf("falta la ruta de la carpeta del plugin")
	}
	abs, err := filepath.Abs(dirPath)
	if err != nil {
		return Installed{}, fmt.Errorf("no pude resolver %q a una ruta absoluta: %w", dirPath, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Installed{}, fmt.Errorf("no encontré la carpeta %s: %w", abs, err)
	}
	if !info.IsDir() {
		return Installed{}, fmt.Errorf("%s no es una carpeta", abs)
	}

	manifest, err := LoadManifest(abs)
	if err != nil {
		return Installed{}, err
	}

	name := nameOverride
	if name == "" {
		name = manifest.Name
	}
	if !apc.IsValidName(name) {
		return Installed{}, fmt.Errorf("no pude derivar un nombre de plugin válido de %q — pasá uno explícito con --name", name)
	}
	if manifest.Name != name {
		return Installed{}, fmt.Errorf(
			"plugin.yaml en %s declara name=%q — usá --name %s (o dejá --name vacío para usar ese nombre tal cual)",
			abs, manifest.Name, manifest.Name,
		)
	}
	if _, err := Get(name); err == nil {
		return Installed{}, fmt.Errorf("ya hay un plugin instalado llamado %q — 'asterion plugin remove %s' primero si querés reinstalarlo", name, name)
	}

	installed := Installed{
		ExternalRef: NewExternalRef(),
		Name:        name,
		Dir:         abs,
		Linked:      true,
		Manifest:    manifest,
		Port:        manifest.Port,
		Status:      "stopped",
		InstalledAt: time.Now(),
	}
	if err := Save(installed); err != nil {
		return Installed{}, err
	}
	return installed, nil
}

// Uninstall para el proceso si está corriendo, borra el repo clonado (o,
// si es un plugin --link, NO borra nada de disco — Dir es la carpeta real
// del usuario, no una copia que Asterion sea dueña de destruir), su config
// cifrada, y el registro en state.json.
func Uninstall(name string) error {
	installed, err := Get(name)
	if err != nil {
		return err
	}
	if installed.Status == "running" {
		if err := Stop(name); err != nil {
			return fmt.Errorf("no pude detener el plugin antes de desinstalarlo: %w", err)
		}
	}
	if installed.Linked {
		if cfgPath, err := configPath(name); err == nil {
			_ = os.Remove(cfgPath)
		}
		return Remove(name)
	}
	if err := os.RemoveAll(installed.Dir); err != nil {
		return err
	}
	if cfgPath, err := configPath(name); err == nil {
		_ = os.Remove(cfgPath)
	}
	return Remove(name)
}
