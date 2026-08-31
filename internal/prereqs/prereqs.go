// Package prereqs clona, al lado de asterion-core, los repos hermanos que
// hacen falta para compilar/correr todo el ecosistema — asterion-lab,
// asterion-language, asterion-plugin-contract (los tres con 'replace ../X'
// en go.mod) y asterion-shared (dependencia editable de Python para
// backend-core). A diferencia de internal/upgrade (que deliberadamente NO
// tiene una lista fija de repos — ver su propio comentario en ListRepos —
// porque solo actualiza lo que YA está en disco), acá sí hace falta un
// catálogo fijo: para clonar algo que todavía no existe hay que saber de
// dónde. Quedan afuera a propósito los plugins oficiales
// (asterion-mail-plugin-basic, asterion-firewall-analysis — ya tienen su
// propio flujo completo vía 'asterion plugin install <url>', a una
// carpeta distinta) y el repo 'asterion' en sí (solo documentación/
// versiones, sin código que compilar).
package prereqs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var catalog = []struct {
	Name string
	URL  string
}{
	{"asterion-lab", "https://github.com/Tarafagat/asterion-lab.git"},
	{"asterion-language", "https://github.com/Tarafagat/asterion-language.git"},
	{"asterion-plugin-contract", "https://github.com/Tarafagat/asterion-plugin-contract.git"},
	{"asterion-shared", "https://github.com/Tarafagat/asterion-shared.git"},
}

// Result es el resultado de procesar UN repo del catálogo — mismo criterio
// que internal/upgrade.Result (Name/Dir/Output/Error), con Cloned en vez
// de Changed.
type Result struct {
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Cloned bool   `json:"cloned"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Install procesa el catálogo entero contra workspaceDir. Un repo que
// falla no corta el resto — mismo criterio que upgrade.UpdateAll — así que
// el único error de retorno es uno que impide seguir con TODOS (falta
// git en el PATH); los fallos por repo van en Result.Error.
func Install(workspaceDir string) ([]Result, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("necesito 'git' en el PATH para clonar los repos hermanos")
	}
	results := make([]Result, 0, len(catalog))
	for _, repo := range catalog {
		results = append(results, installOne(workspaceDir, repo.Name, repo.URL))
	}
	return results, nil
}

func installOne(workspaceDir, name, url string) Result {
	dir := filepath.Join(workspaceDir, name)
	result := Result{Name: name, Dir: dir}

	if info, err := os.Stat(dir); err == nil {
		if !info.IsDir() {
			result.Error = fmt.Sprintf("%s ya existe pero no es una carpeta", dir)
			return result
		}
		if isUsableGitRepo(dir) {
			result.Output = "ya estaba clonado"
			return result
		}
		// .git roto/incompleto (ej. un clone que se cortó a mitad) — se
		// trata como si no existiera: se borra y se clona de nuevo, en
		// vez de reportar "ya estaba" para siempre sobre un repo que en
		// realidad nunca terminó de bajar.
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			result.Error = fmt.Sprintf("%s tiene un .git roto y no lo pude borrar para reintentar: %s", dir, rmErr)
			return result
		}
	}

	cmd := exec.Command("git", "clone", "--depth", "1", url, dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(dir)
		result.Error = strings.TrimSpace(string(out))
		if result.Error == "" {
			result.Error = err.Error()
		}
		return result
	}
	result.Cloned = true
	return result
}

// isUsableGitRepo es más estricto que un simple os.Stat(.git): además
// confirma que HEAD resuelve de verdad (mismo comando que ya usa
// internal/upgrade.currentCommit) — hace falta distinguir "ya está" de
// "un clone que se cortó a mitad", cosa que upgrade.isGitRepo no necesita
// resolver porque nunca clona, solo actualiza lo que ya funciona.
func isUsableGitRepo(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	return cmd.Run() == nil
}
