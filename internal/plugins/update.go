package plugins

import (
	"fmt"
	"os/exec"
	"strings"
)

// UpdateResult es el resultado de intentar actualizar UN plugin instalado
// — una fila del reporte que arma Update/UpdateAll. Nunca lleva un error
// de Go plano: Error queda vacío en éxito para que --json tenga una forma
// estable sea cual sea el resultado.
type UpdateResult struct {
	Name    string `json:"name"`
	Skipped bool   `json:"skipped"`
	Reason  string `json:"reason,omitempty"`
	Changed bool   `json:"changed"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Update hace 'git pull --ff-only' sobre el repo clonado de UN plugin
// instalado por nombre. Nunca hace merge ni fuerza nada — si el repo
// divergió de su rama remota, o tiene cambios propios sin commitear que
// un pull pisaría, git lo rechaza solo (mismo comportamiento que tendría
// el usuario corriendo git a mano) y eso queda en Error, sin tocar nada.
func Update(name string) UpdateResult {
	installed, err := Get(name)
	if err != nil {
		return UpdateResult{Name: name, Error: err.Error()}
	}
	return updateOne(installed)
}

// UpdateAll corre Update sobre todos los plugins instalados, en el orden
// que devuelve List() — un repo que falla (diverge, tiene cambios sin
// commitear, no tiene git) no corta el resto: cada fila de UpdateResult es
// independiente, mismo criterio que ya usa el resto de este paquete de no
// dejar una operación de a uno rota por otra que no tiene nada que ver.
func UpdateAll() ([]UpdateResult, error) {
	list, err := List()
	if err != nil {
		return nil, err
	}
	results := make([]UpdateResult, 0, len(list))
	for _, installed := range list {
		results = append(results, updateOne(installed))
	}
	return results, nil
}

func updateOne(installed Installed) UpdateResult {
	result := UpdateResult{Name: installed.Name}

	// Un plugin --link ni siquiera necesita ser un repo git (ver
	// InstallLinked) — Dir es la carpeta de desarrollo del propio usuario,
	// nunca algo que Asterion deba pisar con un pull automático.
	if installed.Linked {
		result.Skipped = true
		result.Reason = "instalado con --link — no es un clone que Asterion administre, no se actualiza automático"
		return result
	}

	if _, err := exec.LookPath("git"); err != nil {
		result.Error = "necesito 'git' en el PATH para actualizar plugins"
		return result
	}

	before, _ := currentCommit(installed.Dir)

	out, err := pull(installed.Dir)
	result.Output = out
	if err != nil {
		result.Error = out
		if result.Error == "" {
			result.Error = err.Error()
		}
		return result
	}

	after, _ := currentCommit(installed.Dir)
	result.Changed = after != "" && before != after

	// Igual que Start() releyendo el manifest al arrancar: si el pull trajo
	// un plugin.yaml nuevo, state.json no debería quedar con la foto vieja
	// hasta el próximo start. Si la relectura falla (plugin.yaml roto justo
	// después del pull), se deja el último manifest válido conocido en vez
	// de perderlo — mismo criterio que Start().
	if result.Changed {
		if fresh, err := LoadManifest(installed.Dir); err == nil {
			installed.Manifest = fresh
			_ = Save(installed)
		}
	}

	return result
}

// pull corre 'git pull --ff-only' sin argumentos primero (usa el remoto y
// la rama que el repo ya tenga configurados). Si la rama actual no tiene
// upstream (un plugin de terceros clonado o creado sin 'git push -u'),
// git rechaza el pull con "There is no tracking information for the
// current branch" en vez de adivinar — se reintenta una vez, explícito,
// contra origin/<rama actual>, mismo resultado que un 'git pull origin
// main' a mano.
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
