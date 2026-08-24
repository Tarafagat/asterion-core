package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// configPath es BaseDir/config/<name>.json — separado de state.json a
// propósito, para que listar plugins (que sí puede exponerse tal cual a
// frontend-core) nunca ande cerca del archivo que tiene valores cifrados.
func configPath(name string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".json"), nil
}

// loadEncryptedConfig devuelve el mapa key -> valor cifrado tal como está
// en disco (sin descifrar todavía).
func loadEncryptedConfig(name string) (map[string]string, error) {
	path, err := configPath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func saveEncryptedConfig(name string, m map[string]string) error {
	path, err := configPath(name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// SetConfig cifra y guarda cada valor de values — merge sobre lo que ya
// había, así se puede actualizar un solo campo (ej. rotar una clave) sin
// tener que reenviar el resto del formulario.
func SetConfig(name string, values map[string]string) error {
	stored, err := loadEncryptedConfig(name)
	if err != nil {
		return err
	}
	for k, v := range values {
		enc, err := encryptValue(v)
		if err != nil {
			return err
		}
		stored[k] = enc
	}
	return saveEncryptedConfig(name, stored)
}

// GetConfig devuelve la config descifrada — solo para uso interno (pasarla
// como variables de entorno al arrancar el proceso del plugin). Nunca
// exponer el resultado de esto directo a backend-core/frontend-core.
func GetConfig(name string) (map[string]string, error) {
	stored, err := loadEncryptedConfig(name)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(stored))
	for k, enc := range stored {
		plain, err := decryptValue(enc)
		if err != nil {
			return nil, err
		}
		out[k] = plain
	}
	return out, nil
}

// GetConfigMasked es lo que sí puede llegar a frontend-core: qué campos
// están configurados, pero nunca el valor si el campo está marcado
// `secret` en el manifiesto — mismo principio que ya aplica Asterion Cloud
// con las credenciales de cloud_accounts (se cifran, y una vez guardadas
// no se vuelven a mostrar).
func GetConfigMasked(installed Installed) (map[string]string, error) {
	stored, err := loadEncryptedConfig(installed.Name)
	if err != nil {
		return nil, err
	}
	secret := map[string]bool{}
	for _, f := range installed.Manifest.ConfigSchema {
		secret[f.Key] = f.Secret
	}

	out := make(map[string]string, len(stored))
	for k, enc := range stored {
		if secret[k] {
			out[k] = "••••••••"
			continue
		}
		plain, err := decryptValue(enc)
		if err != nil {
			return nil, err
		}
		out[k] = plain
	}
	return out, nil
}
