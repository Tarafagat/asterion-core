package oci

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// apiKeyCredentials es el shape de las credenciales que Asterion guarda al
// conectar una cuenta OCI (ver CREDENTIAL_FIELDS.oci en
// CloudAccountsTab.tsx, y la guía que ya le pasamos al usuario sobre cómo
// sacar cada uno de la consola de OCI).
type apiKeyCredentials struct {
	UserOCID    string
	TenancyOCID string
	Fingerprint string
	PrivateKey  string
}

func parseCredentials(creds map[string]string) (apiKeyCredentials, error) {
	c := apiKeyCredentials{
		UserOCID:    creds["user_ocid"],
		TenancyOCID: creds["tenancy_ocid"],
		Fingerprint: creds["fingerprint"],
		PrivateKey:  creds["private_key"],
	}
	var missing []string
	if c.UserOCID == "" {
		missing = append(missing, "user_ocid")
	}
	if c.TenancyOCID == "" {
		missing = append(missing, "tenancy_ocid")
	}
	if c.Fingerprint == "" {
		missing = append(missing, "fingerprint")
	}
	if c.PrivateKey == "" {
		missing = append(missing, "private_key")
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("oci: faltan credenciales: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

// parsePrivateKey acepta tanto PKCS1 ("BEGIN RSA PRIVATE KEY", lo típico
// de `openssl genrsa`) como PKCS8 ("BEGIN PRIVATE KEY", lo que baja la
// propia consola de OCI al generar el par ahí) — la guía que le pasamos al
// usuario ofrece los dos caminos, así que el adapter no puede asumir uno
// solo.
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no encontré un bloque PEM válido")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("la clave privada no es RSA")
	}
	return rsaKey, nil
}

// signRequest firma req con el esquema de autenticación de OCI (un subset
// de HTTP Signatures — confirmado contra la doc oficial "Request
// Signatures" de Oracle, no de memoria): GET/DELETE firman exactamente
// "(request-target) date host"; POST/PUT con body además firman
// x-content-sha256/content-type/content-length, calculados acá mismo a
// partir de `body` antes de firmar — el caller nunca los setea a mano.
func signRequest(req *http.Request, creds apiKeyCredentials, body []byte) error {
	privKey, err := parsePrivateKey(creds.PrivateKey)
	if err != nil {
		return fmt.Errorf("oci: no pude leer la private_key: %w", err)
	}

	dateValue := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", dateValue)

	headerNames := []string{"(request-target)", "date", "host"}
	headerValues := map[string]string{
		"(request-target)": strings.ToLower(req.Method) + " " + req.URL.RequestURI(),
		"date":             dateValue,
		"host":             req.URL.Host,
	}
	if len(body) > 0 {
		hash := sha256.Sum256(body)
		contentSHA256 := base64.StdEncoding.EncodeToString(hash[:])
		req.Header.Set("X-Content-Sha256", contentSHA256)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", strconv.Itoa(len(body)))
		headerNames = append(headerNames, "x-content-sha256", "content-type", "content-length")
		headerValues["x-content-sha256"] = contentSHA256
		headerValues["content-type"] = "application/json"
		headerValues["content-length"] = strconv.Itoa(len(body))
	}

	lines := make([]string, len(headerNames))
	for i, name := range headerNames {
		lines[i] = name + ": " + headerValues[name]
	}
	signingString := strings.Join(lines, "\n")

	hashed := sha256.Sum256([]byte(signingString))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hashed[:])
	if err != nil {
		return fmt.Errorf("oci: no pude firmar el request: %w", err)
	}

	keyID := creds.TenancyOCID + "/" + creds.UserOCID + "/" + creds.Fingerprint
	req.Header.Set("Authorization", fmt.Sprintf(
		`Signature version="1",headers="%s",keyId="%s",algorithm="rsa-sha256",signature="%s"`,
		strings.Join(headerNames, " "), keyID, base64.StdEncoding.EncodeToString(signature),
	))
	return nil
}
