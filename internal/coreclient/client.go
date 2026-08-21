// Package coreclient es un cliente liviano hacia asterion-core, usado por
// el CLI para consultas de solo lectura (capabilities, proveedores
// registrados) que no requieren pasar por la API/RBAC de Asterion.
package coreclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client habla con asterion-core (no con la API de Asterion). No requiere
// autenticación: las capabilities de un proveedor son metadata pública, no
// un recurso de un proyecto.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// New arma un Client contra baseURL (ej. "http://localhost:8090").
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTPClient: http.DefaultClient}
}

// Providers lista los códigos de proveedor que asterion-core tiene
// registrados (GET /providers).
func (c *Client) Providers() ([]string, error) {
	var out struct {
		Providers []string `json:"providers"`
	}
	if err := c.get("/providers", &out); err != nil {
		return nil, err
	}
	return out.Providers, nil
}

// Capabilities devuelve el mapa capability -> soportada para un proveedor
// (GET /providers/{provider}/capabilities). Es la fuente de
// `asterion capabilities <provider>`.
func (c *Client) Capabilities(provider string) (map[string]bool, error) {
	var out struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := c.get(fmt.Sprintf("/providers/%s/capabilities", provider), &out); err != nil {
		return nil, err
	}
	return out.Capabilities, nil
}

func (c *Client) get(path string, dst any) error {
	resp, err := c.HTTPClient.Get(c.BaseURL + path)
	if err != nil {
		return fmt.Errorf("no se pudo contactar asterion-core (%s): %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("asterion-core respondió %d: %s", resp.StatusCode, string(data))
	}
	return json.Unmarshal(data, dst)
}
