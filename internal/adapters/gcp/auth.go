package gcp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// serviceAccountKey es el shape del JSON que Google genera al crear una
// clave de service account (Cuentas de servicio → Claves → Crear clave
// nueva → JSON, ver la guía que ya le pasamos al usuario) — el mismo
// archivo completo que se pega en el campo "Service Account JSON" al
// conectar una cuenta GCP en Asterion (credentials["service_account_json"]).
type serviceAccountKey struct {
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

const defaultTokenURI = "https://oauth2.googleapis.com/token"

func parseServiceAccountKey(serviceAccountJSON string) (serviceAccountKey, error) {
	var key serviceAccountKey
	if err := json.Unmarshal([]byte(serviceAccountJSON), &key); err != nil {
		return key, fmt.Errorf("gcp: no pude interpretar el JSON de la service account: %w", err)
	}
	if key.PrivateKey == "" || key.ClientEmail == "" {
		return key, fmt.Errorf("gcp: el JSON de la service account no tiene private_key/client_email")
	}
	if key.ProjectID == "" {
		return key, fmt.Errorf("gcp: el JSON de la service account no tiene project_id")
	}
	return key, nil
}

// accessToken implementa el flujo "JWT Bearer" de OAuth2 para service
// accounts (RFC 7523, ver developers.google.com/identity/protocols/oauth2/
// service-account — confirmado contra la doc oficial, no de memoria):
// firma un JWT de corta vida (1 hora) con la private_key RSA de la cuenta
// (RS256) y lo cambia por un access token real contra el token endpoint de
// Google. Sin dependencias nuevas — todo con la stdlib de Go
// (crypto/rsa, crypto/x509, encoding/pem), mismo criterio de "sin SDK
// pesado" que ya usa internal/adapters/vercel (llamadas HTTP crudas).
func accessToken(ctx context.Context, key serviceAccountKey, scope string) (string, error) {
	tokenURI := key.TokenURI
	if tokenURI == "" {
		tokenURI = defaultTokenURI
	}

	privKey, err := parsePrivateKey(key.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("gcp: no pude leer la private_key de la service account: %w", err)
	}

	now := time.Now()
	claims := map[string]any{
		"iss":   key.ClientEmail,
		"scope": scope,
		"aud":   tokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	signedJWT, err := signJWT(claims, key.PrivateKeyID, privKey)
	if err != nil {
		return "", fmt.Errorf("gcp: no pude firmar el JWT: %w", err)
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", signedJWT)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gcp: no se pudo conectar al token endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gcp: el token endpoint respondió %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("gcp: no pude interpretar la respuesta del token endpoint: %w", err)
	}
	if parsed.AccessToken == "" {
		return "", fmt.Errorf("gcp: el token endpoint no devolvió access_token")
	}
	return parsed.AccessToken, nil
}

// parsePrivateKey lee la private_key PEM tal como viene en el JSON de
// Google — siempre PKCS8 ("BEGIN PRIVATE KEY", no "BEGIN RSA PRIVATE KEY").
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no encontré un bloque PEM válido")
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

func base64URLEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// signJWT arma el JWT sin firmar (header.claims en base64url) y lo firma
// con RS256 (SHA256 + PKCS1v15), tal como exige el token endpoint de Google.
// kid (private_key_id del JSON de la service account) identifica CUÁL de
// las claves activas de esa cuenta firmó el token — Google la necesita
// para encontrar el certificado público correcto y verificar la firma.
func signJWT(claims map[string]any, kid string, key *rsa.PrivateKey) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64URLEncode(headerJSON) + "." + base64URLEncode(claimsJSON)

	hashed := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64URLEncode(signature), nil
}
