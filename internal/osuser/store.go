package osuser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ManagedUser es un usuario de sistema que ESTE `asterion` administra en
// ESTA máquina — el mismo criterio que internal/localstore usa para el
// inventario de instancias: un archivo JSON local, sin sesión ni cuenta.
type ManagedUser struct {
	Username  string    `json:"username"`
	Level     Level     `json:"level"`
	Diff      Diff      `json:"diff"`
	CreatedAt time.Time `json:"created_at"`
}

func storePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "asterion")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "os-users.json"), nil
}

// ListManaged devuelve todos los usuarios que este CLI recuerda haber
// aplicado (vacío si nunca se corrió 'local user create').
func ListManaged() ([]ManagedUser, error) {
	path, err := storePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []ManagedUser{}, nil
	}
	if err != nil {
		return nil, err
	}
	var users []ManagedUser
	if err := json.Unmarshal(data, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func saveManaged(users []ManagedUser) error {
	path, err := storePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// RecordApply guarda (o reemplaza, si ya existía) el resultado de un
// Apply exitoso — es lo que 'local user remove' después lee para saber
// exactamente qué Rollback correr.
func RecordApply(diff Diff) error {
	users, err := ListManaged()
	if err != nil {
		return err
	}
	out := users[:0]
	for _, u := range users {
		if u.Username != diff.Username {
			out = append(out, u)
		}
	}
	out = append(out, ManagedUser{Username: diff.Username, Level: diff.Level, Diff: diff, CreatedAt: time.Now()})
	return saveManaged(out)
}

// GetManaged busca un usuario administrado por nombre.
func GetManaged(username string) (ManagedUser, error) {
	users, err := ListManaged()
	if err != nil {
		return ManagedUser{}, err
	}
	for _, u := range users {
		if u.Username == username {
			return u, nil
		}
	}
	return ManagedUser{}, fmt.Errorf("Asterion no tiene registrado ningún usuario %q en esta máquina (ver 'asterion local user list')", username)
}

// RemoveManaged saca un usuario del store local — se llama después de un
// Rollback exitoso.
func RemoveManaged(username string) error {
	users, err := ListManaged()
	if err != nil {
		return err
	}
	out := users[:0]
	found := false
	for _, u := range users {
		if u.Username == username {
			found = true
			continue
		}
		out = append(out, u)
	}
	if !found {
		return fmt.Errorf("Asterion no tiene registrado ningún usuario %q en esta máquina", username)
	}
	return saveManaged(out)
}
