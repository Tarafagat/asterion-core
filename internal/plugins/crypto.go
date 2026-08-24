package plugins

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// La config de un plugin (RUT, ruta a un certificado, una API key) tiene
// que poder recuperarse en texto plano para pasársela al proceso del
// plugin cuando arranca — a diferencia del token de local-auth (que nunca
// necesita volver a leerse, y por eso solo se guarda su hash), acá hace
// falta cifrado reversible. Mismo problema que ya resolvió Asterion Cloud
// con las credenciales de cloud_accounts (Fernet, backend/app/core/
// encryption.py) — la diferencia es que esto corre en la máquina del
// usuario, sin un ENCRYPTION_KEY de servidor que configurar a mano: la
// clave se genera sola la primera vez, igual que local-auth.yaml.

func masterKeyPath() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "master.key"), nil
}

// ensureMasterKey devuelve la clave AES-256 de este equipo, generándola si
// es la primera vez que se necesita. Nunca se muestra ni se loguea — vive
// únicamente en master.key, permisos 0600, igual que local-auth.yaml.
func ensureMasterKey() ([]byte, error) {
	path, err := masterKeyPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		key, decErr := base64.StdEncoding.DecodeString(string(data))
		if decErr != nil || len(key) != 32 {
			return nil, fmt.Errorf("%s está corrupto — no se puede descifrar la config de los plugins ya guardada. Si no tenés ningún plugin configurado todavía, borralo y se genera uno nuevo", path)
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// encryptValue cifra plainText con AES-256-GCM, devolviendo
// base64(nonce || ciphertext) — autocontenido, no hace falta guardar el
// nonce aparte.
func encryptValue(plainText string) (string, error) {
	key, err := ensureMasterKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plainText), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptValue revierte encryptValue.
func decryptValue(encoded string) (string, error) {
	key, err := ensureMasterKey()
	if err != nil {
		return "", err
	}
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("valor cifrado inválido: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", fmt.Errorf("valor cifrado demasiado corto")
	}
	nonce, cipherText := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, cipherText, nil)
	if err != nil {
		return "", fmt.Errorf("no se pudo descifrar (¿master.key distinta a la que se usó para guardar esto?): %w", err)
	}
	return string(plain), nil
}
