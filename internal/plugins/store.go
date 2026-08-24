package plugins

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Installed es un plugin instalado en esta máquina — la fila que
// state.json guarda por plugin. ExternalRef es la identidad estable que
// usa `asterion plugin connect` para vincularlo a un proyecto de Asterion
// Cloud sin duplicarlo, exactamente el mismo rol que cumple el
// inst_xxxxxxxx de localstore.Instance para instancias: un id generado acá
// una sola vez, único y no adivinable (16 bytes de crypto/rand, nunca
// secuencial), que identifica el recurso sea cual sea el modo de
// administración (local o Cloud).
type Installed struct {
	ExternalRef        string    `json:"external_ref"`
	Name               string    `json:"name"`
	Dir                string    `json:"dir"`
	Manifest           Manifest  `json:"manifest"`
	Port               int       `json:"port,omitempty"`
	PID                int       `json:"pid,omitempty"`
	Status             string    `json:"status"` // stopped | running
	ConnectedProjectID int       `json:"connected_project_id,omitempty"`
	InstalledAt        time.Time `json:"installed_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// NewExternalRef genera el identificador local de un plugin nuevo:
// "plg_" + 16 bytes al azar en hex (128 bits — la misma garantía práctica
// de unicidad que ya usa localstore.NewID para instancias, solo que con el
// doble de entropía porque este id puede terminar viajando fuera de esta
// máquina, hacia Asterion Cloud).
func NewExternalRef() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "plg_" + hex.EncodeToString(b)
}

// BaseDir es ~/.config/asterion/plugins — todo lo de este paquete vive
// ahí: repos clonados, config cifrada, el estado, la master key. Es
// deliberado que sea un único directorio autocontenido: clonar
// ~/.config/asterion/ entero a otra máquina (la instalación completa del
// CLI) alcanza para reproducir los plugins instalados ahí, sin un
// mecanismo de export/import aparte.
func BaseDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "asterion", "plugins")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// ReposDir es donde se clona cada plugin (BaseDir/repos/<name>) — separado
// del estado y la config para que un `git pull` de actualización nunca
// pise nada que Asterion mismo escribió.
func ReposDir(name string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "repos", name)
	return dir, nil
}

func statePath() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "state.json"), nil
}

// List devuelve todos los plugins instalados (vacío si nunca se instaló
// ninguno).
func List() ([]Installed, error) {
	path, err := statePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []Installed{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Installed
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func saveAll(list []Installed) error {
	path, err := statePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Get busca un plugin instalado por nombre.
func Get(name string) (Installed, error) {
	list, err := List()
	if err != nil {
		return Installed{}, err
	}
	for _, p := range list {
		if p.Name == name {
			return p, nil
		}
	}
	return Installed{}, fmt.Errorf("no hay ningún plugin instalado llamado %q (ver 'asterion plugin list')", name)
}

// Save agrega o reemplaza (por Name) un registro de plugin instalado.
func Save(p Installed) error {
	list, err := List()
	if err != nil {
		return err
	}
	p.UpdatedAt = time.Now()
	found := false
	for i, existing := range list {
		if existing.Name == p.Name {
			list[i] = p
			found = true
			break
		}
	}
	if !found {
		list = append(list, p)
	}
	return saveAll(list)
}

// Remove borra el registro de un plugin instalado (no borra el directorio
// clonado — eso lo hace el llamador explícitamente, ver Uninstall).
func Remove(name string) error {
	list, err := List()
	if err != nil {
		return err
	}
	out := list[:0]
	found := false
	for _, p := range list {
		if p.Name == name {
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		return fmt.Errorf("no hay ningún plugin instalado llamado %q", name)
	}
	return saveAll(out)
}
