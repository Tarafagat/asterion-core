package plugins

import (
	"path/filepath"

	"asterion-core/internal/secretbox"
)

// La config de un plugin (RUT, ruta a un certificado, una API key) tiene
// que poder recuperarse en texto plano para pasársela al proceso del
// plugin cuando arranca — a diferencia del token de local-auth (que nunca
// necesita volver a leerse, y por eso solo se guarda su hash), acá hace
// falta cifrado reversible. Mismo problema que ya resolvió Asterion Cloud
// con las credenciales de cloud_accounts (Fernet, backend/app/core/
// encryption.py) — la diferencia es que esto corre en la máquina del
// usuario, sin un ENCRYPTION_KEY de servidor que configurar a mano: la
// clave se genera sola la primera vez, igual que local-auth.yaml. La
// implementación en sí vive en internal/secretbox (compartida con
// internal/tunnel) — acá solo se fija dónde vive la clave de este
// subsistema puntual.

func masterKeyPath() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "master.key"), nil
}

func encryptValue(plainText string) (string, error) {
	path, err := masterKeyPath()
	if err != nil {
		return "", err
	}
	key, err := secretbox.EnsureKey(path)
	if err != nil {
		return "", err
	}
	return secretbox.Encrypt(key, plainText)
}

func decryptValue(encoded string) (string, error) {
	path, err := masterKeyPath()
	if err != nil {
		return "", err
	}
	key, err := secretbox.EnsureKey(path)
	if err != nil {
		return "", err
	}
	return secretbox.Decrypt(key, encoded)
}
