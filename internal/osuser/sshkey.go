package osuser

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
)

// GenerateKeyPair genera un par de claves Ed25519 nuevo, sin ninguna
// dependencia externa (mismo criterio "stdlib primero" que ya usa el
// adapter de GCP para su flujo OAuth2). Devuelve la clave privada en PEM
// PKCS8 (formato que ssh/sshd modernos aceptan igual que el propio de
// OpenSSH) y la línea pública lista para authorized_keys.
func GenerateKeyPair(comment string) (privatePEM string, publicLine string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("no se pudo generar el par de claves: %w", err)
	}

	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	privatePEM = string(pem.EncodeToMemory(block))

	publicLine = encodeSSHEd25519PublicKey(pub, comment)
	return privatePEM, publicLine, nil
}

// encodeSSHEd25519PublicKey arma la línea "ssh-ed25519 AAAA... comentario"
// codificando el blob en el formato de wire de SSH (RFC 4253 §6.6): cada
// campo es un uint32 big-endian con la longitud seguido de los bytes.
func encodeSSHEd25519PublicKey(pub ed25519.PublicKey, comment string) string {
	const keyType = "ssh-ed25519"

	blob := make([]byte, 0, 4+len(keyType)+4+len(pub))
	blob = appendSSHString(blob, []byte(keyType))
	blob = appendSSHString(blob, pub)

	line := keyType + " " + base64.StdEncoding.EncodeToString(blob)
	if comment != "" {
		line += " " + comment
	}
	return line
}

func appendSSHString(dst []byte, s []byte) []byte {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(s)))
	dst = append(dst, lenBuf[:]...)
	dst = append(dst, s...)
	return dst
}
