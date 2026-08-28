package tunnel

import (
	"encoding/json"
	"os"
	"path/filepath"

	"asterion-core/internal/secretbox"
)

// Config es lo que se guarda entre corridas: si hay un token de Cloudflare
// Tunnel configurado, `local tunnel start` lo usa automáticamente en vez
// de pedirlo cada vez — mismo espíritu que `asterion plugin config set`,
// pero para este subsistema puntual (clave propia, ver masterKeyPath, sin
// tocar la de internal/plugins).
type Config struct {
	Token string `json:"token,omitempty"` // cifrado en disco, en texto plano en memoria
}

func configPath() (string, error) {
	base, err := baseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tunnel-config.json"), nil
}

func masterKeyPath() (string, error) {
	base, err := baseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tunnel-master.key"), nil
}

type storedConfig struct {
	TokenEncrypted string `json:"token_encrypted,omitempty"`
}

// SetToken cifra y guarda el token de un túnel con nombre (Cloudflare
// Tunnel) — lo que `cloudflared tunnel run --token ...` necesita. Pasar
// una cadena vacía borra lo guardado (vuelve a modo quick tunnel).
func SetToken(token string) error {
	if token == "" {
		return clearStored()
	}
	path, err := masterKeyPath()
	if err != nil {
		return err
	}
	key, err := secretbox.EnsureKey(path)
	if err != nil {
		return err
	}
	enc, err := secretbox.Encrypt(key, token)
	if err != nil {
		return err
	}
	return save(storedConfig{TokenEncrypted: enc})
}

// LoadConfig devuelve la config descifrada — Token vacío si nunca se
// configuró un token (modo quick tunnel por default).
func LoadConfig() (Config, error) {
	stored, found, err := load()
	if err != nil || !found || stored.TokenEncrypted == "" {
		return Config{}, err
	}
	path, err := masterKeyPath()
	if err != nil {
		return Config{}, err
	}
	key, err := secretbox.EnsureKey(path)
	if err != nil {
		return Config{}, err
	}
	token, err := secretbox.Decrypt(key, stored.TokenEncrypted)
	if err != nil {
		return Config{}, err
	}
	return Config{Token: token}, nil
}

func clearStored() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func save(c storedConfig) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func load() (storedConfig, bool, error) {
	path, err := configPath()
	if err != nil {
		return storedConfig{}, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return storedConfig{}, false, nil
	}
	if err != nil {
		return storedConfig{}, false, err
	}
	var c storedConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return storedConfig{}, false, err
	}
	return c, true, nil
}
