package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// iaasBaseURL/identityBaseURL son var (no const) a propósito, mismo
// criterio que computeAPIBaseURL en internal/adapters/gcp: OCI tiene un
// host por región (ej. "iaas.us-ashburn-1.oraclecloud.com"), así que acá
// son funciones en vez de una URL fija — los tests las reemplazan por una
// que devuelve la URL de un httptest.Server, sin tocar ninguna llamada real.
var iaasBaseURL = func(region string) string { return "https://iaas." + region + ".oraclecloud.com" }
var identityBaseURL = func(region string) string { return "https://identity." + region + ".oraclecloud.com" }

// doSigned arma, firma (ver signRequest en auth.go) y ejecuta un request
// contra la API de OCI. body va nil para GET/DELETE. Devuelve el body
// crudo de la respuesta, el status code, y los headers (algunos endpoints,
// como ListInstances, paginan vía el header de respuesta "opc-next-page",
// no vía un campo del body) — la decodificación del JSON queda a cargo de
// cada caller, cada endpoint de OCI tiene su propio shape.
func doSigned(ctx context.Context, method, requestURL string, creds apiKeyCredentials, body []byte) ([]byte, int, http.Header, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return nil, 0, nil, err
	}
	if err := signRequest(req, creds, body); err != nil {
		return nil, 0, nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("oci: no se pudo conectar (%s): %w", requestURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, 0, nil, err
	}
	return respBody, resp.StatusCode, resp.Header, nil
}

func decodeJSON(body []byte, dst any) error {
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("oci: no pude interpretar la respuesta: %w", err)
	}
	return nil
}

// resolveCompartmentID: si las credenciales no traen compartment_id
// explícito (no hay ningún campo para esto todavía en el formulario de
// Asterion, ver CloudAccountsTab.tsx), se usa el compartment raíz — el
// mismo id que la tenancy. Es válido para cualquier cuenta y el único que
// existe siempre sin que el usuario tenga que crear compartments propios
// primero (el caso típico de una cuenta personal, como la que se usó para
// probar esto en vivo).
func resolveCompartmentID(rawCredentials map[string]string, creds apiKeyCredentials) string {
	if compartmentID := rawCredentials["compartment_id"]; compartmentID != "" {
		return compartmentID
	}
	return creds.TenancyOCID
}
