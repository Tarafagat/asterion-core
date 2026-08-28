// Package secretbox es el cifrado reversible AES-256-GCM que ya usaba
// internal/plugins (RUT, API keys, certificados de un plugin) — extraído
// acá para que cualquier otro subsistema que necesite guardar un secreto
// propio en disco (ej. internal/tunnel, un token de Cloudflare) lo haga
// con la misma implementación probada, en vez de reinventar el cifrado
// una segunda vez. Cada caller elige dónde vive su propia clave (EnsureKey
// recibe la ruta) — no hay una única clave compartida por todo el CLI a
// propósito: así el master.key que ya tienen instalaciones existentes de
// internal/plugins sigue funcionando exactamente igual, sin migración.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

// EnsureKey devuelve la clave AES-256 guardada en keyPath, generándola si
// es la primera vez que se necesita. Nunca se muestra ni se loguea.
func EnsureKey(keyPath string) ([]byte, error) {
	data, err := os.ReadFile(keyPath)
	if err == nil {
		key, decErr := base64.StdEncoding.DecodeString(string(data))
		if decErr != nil || len(key) != 32 {
			return nil, fmt.Errorf("%s está corrupto — no se puede descifrar lo ya guardado con esta clave. Si no hay nada guardado todavía que dependa de ella, borrala y se genera una nueva", keyPath)
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
	if err := os.WriteFile(keyPath, []byte(encoded), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// Encrypt cifra plainText con AES-256-GCM, devolviendo
// base64(nonce || ciphertext) — autocontenido, no hace falta guardar el
// nonce aparte.
func Encrypt(key []byte, plainText string) (string, error) {
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

// Decrypt revierte Encrypt.
func Decrypt(key []byte, encoded string) (string, error) {
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
		return "", fmt.Errorf("no se pudo descifrar (¿clave distinta a la que se usó para guardar esto?): %w", err)
	}
	return string(plain), nil
}
