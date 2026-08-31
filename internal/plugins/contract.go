package plugins

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// contractDirName es el nombre fijo de la carpeta del contrato compartido
// dentro de ReposRoot — mismo nivel que cada plugin instalado (ReposDir),
// así que el 'replace .../asterion-plugin-contract => ../asterion-plugin-contract'
// que cada plugin declara en su PROPIO go.mod (relativo a su carpeta)
// resuelve acá sin importar de qué plugin se trate: "../" desde cualquier
// ReposRoot/<lo-que-sea> siempre da ReposRoot. Una sola copia compartida,
// no una por plugin.
const contractDirName = "asterion-plugin-contract"
const contractRepoURL = "https://github.com/Tarafagat/asterion-plugin-contract.git"

// ReposRoot es BaseDir/repos — la carpeta que contiene tanto cada plugin
// clonado (ReposDir) como, al lado, la única copia compartida del
// contrato que todos ellos necesitan para compilar.
func ReposRoot() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "repos"), nil
}

// EnsureContractRepo clona (o repara) la copia compartida de
// asterion-plugin-contract en ReposRoot — todo plugin real la necesita
// para compilar, literalmente por ser un plugin que implementa ese
// contrato (mismo problema que resuelve internal/prereqs para los repos
// hermanos de asterion-core, acá un nivel más abajo: sin esto, 'go build'
// dentro de un plugin clonado falla con "replacement directory
// ../asterion-plugin-contract does not exist", visto en vivo en una
// instancia recién clonada).
//
// Idempotente y auto-reparable: si ya está clonada y HEAD resuelve, no
// toca nada; si la carpeta existe pero con un .git roto (un clone que se
// cortó a mitad de camino), la borra y clona de nuevo en vez de quedar
// reportando éxito para siempre sobre algo que nunca terminó de bajar
// (mismo criterio que internal/prereqs.installOne).
func EnsureContractRepo() error {
	root, err := ReposRoot()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, contractDirName)

	if info, statErr := os.Stat(dir); statErr == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s ya existe pero no es una carpeta", dir)
		}
		if isUsableGitRepo(dir) {
			return nil
		}
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			return fmt.Errorf("%s tiene un .git roto y no lo pude borrar para reintentar: %w", dir, rmErr)
		}
	}

	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("necesito 'git' en el PATH para clonar asterion-plugin-contract")
	}
	cmd := exec.Command("git", "clone", "--depth", "1", contractRepoURL, dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(dir)
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git clone de asterion-plugin-contract falló: %s", msg)
	}
	return nil
}

// UpdateContractRepo hace 'git pull --ff-only' sobre la copia compartida
// del contrato (ver EnsureContractRepo), si ya existe — nunca la clona,
// para eso está EnsureContractRepo. present=false si todavía no existe
// (nunca se instaló ningún plugin en esta máquina): no es un error, no
// hay nada que actualizar todavía.
//
// A diferencia de updateOne, esta carpeta no tiene plugin.yaml ni una
// fila en state.json que releer/guardar — no es un plugin instalado, es
// una dependencia compartida que todos usan para compilar.
func UpdateContractRepo() (result UpdateResult, present bool, err error) {
	root, err := ReposRoot()
	if err != nil {
		return UpdateResult{}, false, err
	}
	dir := filepath.Join(root, contractDirName)
	if !isUsableGitRepo(dir) {
		return UpdateResult{}, false, nil
	}

	result = UpdateResult{Name: contractDirName}
	if _, lookErr := exec.LookPath("git"); lookErr != nil {
		result.Error = "necesito 'git' en el PATH"
		return result, true, nil
	}

	before, _ := currentCommit(dir)
	out, pullErr := pull(dir)
	result.Output = out
	if pullErr != nil {
		result.Error = out
		if result.Error == "" {
			result.Error = pullErr.Error()
		}
		return result, true, nil
	}
	after, _ := currentCommit(dir)
	result.Changed = after != "" && before != after
	return result, true, nil
}

func isUsableGitRepo(dir string) bool {
	_, err := currentCommit(dir)
	return err == nil
}
