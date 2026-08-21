// Package localstore administra un inventario de instancias/hosts SSH
// puramente local (~/.config/asterion/instances.json). No requiere sesión
// ni pega contra la API de Asterion — es para que cualquier usuario del
// CLI pueda ver, agregar y conectarse a SUS máquinas personales aunque
// nunca se loguee a Asterion Cloud. `asterion cloud install-agent` es lo
// que después conecta uno de estos hosts (o la máquina actual) con un
// proyecto real de Asterion Cloud.
package localstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Instance es un perfil de conexión guardado localmente. ID es la
// identidad estable del recurso (inst_xxxxxxxx, generada una sola vez acá)
// — es lo que 'asterion cloud connect'/'install-agent' manda como
// external_ref para vincular esta MISMA instancia a un proyecto de
// Asterion Cloud sin duplicarla. Local y Cloud son dos modos de
// administración sobre el mismo ID, nunca dos filas distintas.
type Instance struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Host         string    `json:"host"`
	Port         int       `json:"port"`
	User         string    `json:"user"`
	IdentityFile string    `json:"identity_file,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// NewID genera un id local nuevo (inst_ + 8 bytes al azar en hex).
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "inst_" + hex.EncodeToString(b)
}

func filePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "asterion")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "instances.json"), nil
}

// List devuelve todas las instancias guardadas localmente (vacío si nunca
// se agregó ninguna).
func List() ([]Instance, error) {
	path, err := filePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []Instance{}, nil
	}
	if err != nil {
		return nil, err
	}
	var instances []Instance
	if err := json.Unmarshal(data, &instances); err != nil {
		return nil, err
	}
	return instances, nil
}

func save(instances []Instance) error {
	path, err := filePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(instances, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Add agrega una instancia nueva. Falla si ya existe una con ese nombre.
func Add(instance Instance) error {
	instances, err := List()
	if err != nil {
		return err
	}
	for _, existing := range instances {
		if existing.Name == instance.Name {
			return fmt.Errorf("ya existe una instancia local llamada %q", instance.Name)
		}
	}
	if instance.ID == "" {
		instance.ID = NewID()
	}
	instance.CreatedAt = time.Now()
	instances = append(instances, instance)
	return save(instances)
}

// Get busca una instancia local por nombre o por id (inst_xxxxxxxx).
func Get(nameOrID string) (Instance, error) {
	instances, err := List()
	if err != nil {
		return Instance{}, err
	}
	for _, instance := range instances {
		if instance.Name == nameOrID || instance.ID == nameOrID {
			return instance, nil
		}
	}
	return Instance{}, fmt.Errorf("no hay ninguna instancia local %q (ver 'asterion instances list')", nameOrID)
}

// Remove borra una instancia local por nombre o id.
func Remove(nameOrID string) error {
	instances, err := List()
	if err != nil {
		return err
	}
	out := instances[:0]
	found := false
	for _, instance := range instances {
		if instance.Name == nameOrID || instance.ID == nameOrID {
			found = true
			continue
		}
		out = append(out, instance)
	}
	if !found {
		return fmt.Errorf("no hay ninguna instancia local %q", nameOrID)
	}
	return save(out)
}
