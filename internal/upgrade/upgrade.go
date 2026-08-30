// Package upgrade actualiza (git pull) los repos del propio ecosistema
// Asterion en el workspace de desarrollo — asterion-core, asterion-language,
// asterion-plugin-contract, etc., los que declaran 'replace X =>
// ../<repo>' entre sí en su go.mod. A diferencia de internal/plugins (que
// administra plugins de TERCEROS instalados en ~/.config/asterion/plugins/,
// clonados uno por uno con 'plugin install'), este paquete nunca toca esa
// carpeta ni asume una ruta fija: busca hermanos de asterion-core en el
// sistema de archivos a partir de dónde se lo corre, para no atar el
// mecanismo a la organización de carpetas de una persona en particular
// (hoy ~/Desktop/asterion, pero cualquier carpeta con asterion-core adentro
// sirve). asterion-plugin-contract es un repo más de esta lista, ni
// especial ni tocado dos veces: aunque asterion-core Y asterion-language
// lo referencien cada uno con su propio 'replace', en el filesystem es una
// sola carpeta — ListRepos la lista una sola vez.
package upgrade

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Result es el resultado de actualizar UN repo del ecosistema.
type Result struct {
	Name    string `json:"name"`
	Dir     string `json:"dir"`
	Changed bool   `json:"changed"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// FindWorkspace busca, desde startDir hacia arriba (startDir="" usa el
// directorio de trabajo actual), el primer directorio que tenga una
// subcarpeta "asterion-core" con su propio .git — ese es el ancla del
// workspace de desarrollo. No hay ruta fija hardcodeada: cualquier
// checkout de asterion-core sirve de referencia, en cualquier máquina.
func FindWorkspace(startDir string) (string, error) {
	dir := startDir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	for {
		if isGitRepo(filepath.Join(dir, "asterion-core")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf(
		"no encontré ninguna carpeta con 'asterion-core' adentro subiendo desde %s — "+
			"corré esto desde tu workspace de desarrollo, o pasá --dir",
		startDir,
	)
}

func isGitRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && info != nil
}

// ListRepos devuelve, en orden alfabético, los repos del ecosistema
// Asterion directamente dentro de workspaceDir: cualquier subcarpeta cuyo
// nombre empiece con "asterion" y tenga su propio .git. Sin lista fija a
// mano — un repo nuevo (asterion-X) aparece solo con clonarlo ahí, sin
// tocar código acá.
func ListRepos(workspaceDir string) ([]string, error) {
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return nil, err
	}
	var repos []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "asterion") {
			continue
		}
		if isGitRepo(filepath.Join(workspaceDir, e.Name())) {
			repos = append(repos, e.Name())
		}
	}
	sort.Strings(repos)
	return repos, nil
}

// UpdateAll hace 'git pull --ff-only' sobre cada repo de ListRepos. Mismo
// criterio que plugins.UpdateAll: un repo que falla (cambios propios sin
// commitear, rama divergida) no corta el resto — cada fila de Result es
// independiente.
func UpdateAll(workspaceDir string) ([]Result, error) {
	names, err := ListRepos(workspaceDir)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no encontré ningún repo 'asterion*' con .git dentro de %s", workspaceDir)
	}
	results := make([]Result, 0, len(names))
	for _, name := range names {
		results = append(results, updateOne(workspaceDir, name))
	}
	return results, nil
}

// Update actualiza un único repo por nombre (debe ser una subcarpeta
// directa de workspaceDir con su propio .git).
func Update(workspaceDir, name string) (Result, error) {
	dir := filepath.Join(workspaceDir, name)
	if !isGitRepo(dir) {
		return Result{}, fmt.Errorf("%s no existe o no es un repo git (ver 'asterion upgrade --list')", dir)
	}
	return updateOne(workspaceDir, name), nil
}

func updateOne(workspaceDir, name string) Result {
	dir := filepath.Join(workspaceDir, name)
	result := Result{Name: name, Dir: dir}

	if _, err := exec.LookPath("git"); err != nil {
		result.Error = "necesito 'git' en el PATH"
		return result
	}

	before, _ := currentCommit(dir)

	out, err := pull(dir)
	result.Output = out
	if err != nil {
		result.Error = out
		if result.Error == "" {
			result.Error = err.Error()
		}
		return result
	}

	after, _ := currentCommit(dir)
	result.Changed = after != "" && before != after
	return result
}

// pull corre 'git pull --ff-only' sin argumentos primero (usa el remoto y
// la rama que el repo ya tenga configurados). Si la rama actual no tiene
// upstream (pasa en algunos de estos repos hermanos, clonados o creados
// sin 'git push -u' — confirmado en vivo con asterion-language: git
// rechaza el pull con "There is no tracking information for the current
// branch" en vez de adivinar), se reintenta una vez, explícito, contra
// origin/<rama actual> — el mismo resultado que un 'git pull origin main'
// a mano, sin necesitar que cada repo tenga el tracking ya configurado de
// antemano.
func pull(dir string) (string, error) {
	cmd := exec.Command("git", "pull", "--ff-only")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err == nil || !strings.Contains(output, "no tracking information") {
		return output, err
	}

	branch, branchErr := currentBranch(dir)
	if branchErr != nil {
		return output, err
	}
	cmd = exec.Command("git", "pull", "--ff-only", "origin", branch)
	cmd.Dir = dir
	out2, err2 := cmd.CombinedOutput()
	return strings.TrimSpace(string(out2)), err2
}

func currentBranch(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --abbrev-ref HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func currentCommit(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
