// Package cliconfig persiste la configuración y la sesión del CLI en
// ~/.config/asterion/ (config.json + credentials.json), igual que hacen
// gh/aws-cli/kubectl.
package cliconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Config son los ajustes persistentes del CLI (no secretos): a dónde
// apuntan la API de Asterion y asterion-core. Se guarda en
// ~/.config/asterion/config.json. Cómo se autentica esa sesión por dentro
// (Firebase u otra cosa) es un detalle interno de la API de Asterion — el
// CLI solo conoce /auth/cli/* y una sesión genérica (ver Credentials).
type Config struct {
	APIBaseURL     string `json:"api_base_url"`
	CoreServiceURL string `json:"core_service_url"`
}

// Credentials es la sesión activa del CLI, guardada en
// ~/.config/asterion/credentials.json (permisos 0600). `asterion cloud
// login` la crea; el resto de los comandos la leen y la renuevan solos
// contra POST /auth/cli/refresh cuando está por vencer.
type Credentials struct {
	Email        string    `json:"email"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "asterion")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

// Load lee la Config guardada, o devuelve los defaults (localhost) si
// todavía no se guardó ninguna.
func Load() (Config, error) {
	cfg := Config{
		APIBaseURL:     "http://localhost:8000",
		CoreServiceURL: "http://localhost:8090",
	}
	d, err := dir()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(filepath.Join(d, "config.json"))
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Save persiste cfg en ~/.config/asterion/config.json.
func Save(cfg Config) error {
	d, err := dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "config.json"), data, 0o600)
}

// LoadCredentials lee la sesión guardada. Devuelve error si no hay ninguna
// (el llamador debe interpretarlo como "corré 'asterion cloud login'").
func LoadCredentials() (Credentials, error) {
	var creds Credentials
	d, err := dir()
	if err != nil {
		return creds, err
	}
	data, err := os.ReadFile(filepath.Join(d, "credentials.json"))
	if err != nil {
		return creds, err
	}
	err = json.Unmarshal(data, &creds)
	return creds, err
}

// SaveCredentials persiste la sesión en ~/.config/asterion/credentials.json.
func SaveCredentials(creds Credentials) error {
	d, err := dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "credentials.json"), data, 0o600)
}
