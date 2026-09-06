// Package coreclient es un cliente liviano hacia asterion-core, usado por
// el CLI tanto para consultas de solo lectura (capabilities, proveedores
// registrados) como, desde CreateInstance, para la primera escritura real
// (crear infraestructura vía un adapter) — ninguna de las dos pasa por la
// API/RBAC de Asterion: capabilities es metadata pública, y CreateInstance
// ya recibe credenciales explícitas en el propio spec (ver
// cmd/asterion/language.go:runLanguageApply).
package coreclient

import (
	"bytes"
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

// CreateInstance crea una instancia real vía el adapter de provider
// (POST /adapters/{provider}/instances) — usado hoy por
// 'asterion language apply' (ver providerspec.CompileInstances del lado
// de asterion-language para de dónde sale spec). spec ya debe traer
// "credentials" con lo que ese adapter necesite (para GCP,
// "service_account_json").
func (c *Client) CreateInstance(provider string, spec map[string]any) (map[string]any, error) {
	var out map[string]any
	err := c.post(fmt.Sprintf("/adapters/%s/instances", provider), spec, &out)
	return out, err
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

func (c *Client) post(path string, body, dst any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient.Post(c.BaseURL+path, "application/json", bytes.NewReader(payload))
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
