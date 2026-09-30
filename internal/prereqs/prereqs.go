// Package prereqs clona, al lado de asterion-core, los repos hermanos que
// hacen falta para compilar/correr todo el ecosistema — asterion-lab,
// asterion-language, asterion-plugin-contract y
// asterion-graph-cognitive-architecture (los cuatro con 'replace ../X'
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
	{"asterion-graph-cognitive-architecture", "https://github.com/Tarafagat/asterion-graph-cognitive-architecture.git"},
}

// Result es el resultado de procesar UN repo del catálogo — mismo criterio
// que internal/upgrade.Result (Name/Dir/Output/Error), con Cloned en vez
// de Changed.
type Result struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
	// Cloned: no existía y se clonó. Updated: ya existía y se
	// fast-forwardeó a lo último. Los dos en false con Error vacío
	// significa que ya estaba al día, o que se dejó intacto a propósito
	// (ver Output: cambios locales sin commitear, detached HEAD).
	Cloned  bool   `json:"cloned"`
	Updated bool   `json:"updated"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
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

// updateExisting fast-forwardea un repo hermano que YA estaba en disco.
// No alcanza con que exista: un hermano viejo rompe el build con un
// error críptico adentro de otro repo (visto en vivo: asterion-language
// desactualizado -> "undefined: agcaspec.RoleDecl" al compilar
// asterion-graph-cognitive-architecture).
//
// Nunca se pisa trabajo de nadie: si hay cambios sin commitear, o el
// repo está en detached HEAD, se deja exactamente como está y se dice
// por qué. Un fallo al actualizar tampoco corta el proceso — se reporta
// y se sigue con lo que haya en disco, mismo criterio que un clone
// fallido.
func updateExisting(dir string, result Result) Result {
	if out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); err == nil && len(strings.TrimSpace(string(out))) > 0 {
		result.Output = "tiene cambios locales sin commitear, no se tocó"
		return result
	}

	branchOut, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	branch := strings.TrimSpace(string(branchOut))
	if err != nil || branch == "" || branch == "HEAD" {
		result.Output = "en detached HEAD (o sin rama), no se tocó"
		return result
	}

	// Rama explícita en vez de confiar en el upstream: un clone que
	// quedó sin tracking info haría fallar un 'git pull' pelado con
	// "There is no tracking information for the current branch".
	before, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	out, err := exec.Command("git", "-C", dir, "pull", "--ff-only", "origin", branch).CombinedOutput()
	if err != nil {
		result.Output = "ya estaba, pero no se pudo actualizar: " + strings.TrimSpace(string(out))
		return result
	}
	after, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()

	if strings.TrimSpace(string(before)) != strings.TrimSpace(string(after)) {
		result.Updated = true
		result.Output = "actualizado a " + shortSHA(string(after))
		return result
	}
	result.Output = "ya estaba, al día"
	return result
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
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
			return updateExisting(dir, result)
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
