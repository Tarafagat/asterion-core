// Package localauth es el login de `asterion local serve` — reemplaza el
// login con Google/Firebase que tenía antes. Ya no hace falta un proyecto
// de Firebase para usar el dashboard local: `asterion local serve` genera
// un token propio la primera vez, lo guarda (hasheado, nunca en texto
// plano) en un YAML en ~/.config/asterion/local-auth.yaml con permisos
// 0600 (solo tu usuario del sistema operativo puede leerlo), y lo muestra
// una única vez en la terminal — el mismo patrón que ya usan las API keys
// de instancia (ssh_repo.create_api_key: se genera, se muestra una vez, y
// de ahí en más solo se guarda el hash).
//
// backend-core (Python) lee el mismo archivo para verificar el token que
// pega el usuario en el navegador — un solo lugar donde vive el secreto,
// nunca duplicado entre el CLI y el dashboard.
package localauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// File es el contenido de ~/.config/asterion/local-auth.yaml.
type File struct {
	TokenHash string    `yaml:"token_hash"`
	CreatedAt time.Time `yaml:"created_at"`
}

func path() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "asterion")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "local-auth.yaml"), nil
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func load() (File, string, error) {
	p, err := path()
	if err != nil {
		return File{}, "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return File{}, p, err
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return File{}, p, err
	}
	return f, p, nil
}

func save(f File) error {
	p, err := path()
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// EnsureToken es lo que corre `asterion local serve` al arrancar: si ya
// hay un token guardado, no lo toca (rawToken viene vacío — no se puede
// recuperar un valor del que solo se guardó el hash) y sigue funcionando
// con el que ya existía. Si es la primera vez, genera uno nuevo y lo
// devuelve en texto plano UNA sola vez para que el CLI lo imprima.
func EnsureToken() (rawToken string, isNew bool, err error) {
	if _, _, loadErr := load(); loadErr == nil {
		return "", false, nil
	}
	token, err := newToken()
	if err != nil {
		return "", false, err
	}
	if err := save(File{TokenHash: hash(token), CreatedAt: time.Now()}); err != nil {
		return "", false, err
	}
	return token, true, nil
}

// Rotate invalida el token anterior (si había uno) y genera uno nuevo —
// para cuando el usuario perdió el que tenía o sospecha que se filtró.
func Rotate() (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	if err := save(File{TokenHash: hash(token), CreatedAt: time.Now()}); err != nil {
		return "", err
	}
	return token, nil
}

// Status describe el archivo sin exponer nunca el secreto — para
// `asterion local auth status`.
type Status struct {
	Configured bool      `json:"configured"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	Path       string    `json:"path"`
}

func GetStatus() (Status, error) {
	f, p, err := load()
	if err != nil {
		if os.IsNotExist(err) {
			return Status{Configured: false, Path: p}, nil
		}
		return Status{}, err
	}
	return Status{Configured: true, CreatedAt: f.CreatedAt, Path: p}, nil
}

// Verify existe para que el propio CLI (o un test) pueda confirmar que un
// token es válido sin tener que llamar a backend-core — backend-core hace
// la misma comparación de forma independiente, leyendo el mismo archivo.
func Verify(token string) (bool, error) {
	f, _, err := load()
	if err != nil {
		return false, err
	}
	return hash(token) == f.TokenHash, nil
}

// FormatFirstRun es el bloque que se imprime en `asterion local serve`
// cuando se generó un token nuevo — solo esta vez va a mostrarse en texto
// plano.
func FormatFirstRun(token string) string {
	return fmt.Sprintf(
		"Token de acceso generado: %s\nGuardado (hasheado) en ~/.config/asterion/local-auth.yaml — este token no se vuelve a mostrar, guardalo ahora.\nPegalo en el dashboard para entrar. Si lo perdés: 'asterion local auth rotate'.",
		token,
	)
}
