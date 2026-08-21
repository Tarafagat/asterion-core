// Package apiclient es el cliente Go de la API de Asterion (FastAPI). El
// CLI nunca le habla directo a un proveedor cloud: toda mutación de estado
// pasa por acá, para que la API siga siendo la que aplica permisos (RBAC) y
// deja rastro en auditoría — el Core (adapters) es un detalle de
// implementación de "cómo se aplica un paso", no reemplaza esas reglas.
package apiclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client es el cliente HTTP de la API de Asterion. TokenFunc se llama antes
// de cada request para obtener un access token de sesión vigente — en el
// CLI eso implica renovarlo si venció (ver cmd/asterion/main.go:apiToken).
// Cómo la API arma o valida ese token es un detalle interno suyo; el
// cliente solo sabe que existe /auth/cli/*.
type Client struct {
	BaseURL    string
	TokenFunc  func() (string, error)
	HTTPClient *http.Client
}

// New arma un Client autenticado contra baseURL (ej. "http://localhost:8000"),
// resolviendo el token de autenticación con tokenFunc en cada llamada.
func New(baseURL string, tokenFunc func() (string, error)) *Client {
	return &Client{BaseURL: baseURL, TokenFunc: tokenFunc, HTTPClient: http.DefaultClient}
}

// NewUnauthenticated arma un Client sin sesión, para los únicos endpoints
// que no la requieren: el propio flujo de login (/auth/cli/request-code,
// /auth/cli/verify-code, /auth/cli/refresh).
func NewUnauthenticated(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTPClient: http.DefaultClient}
}

// APIError envuelve una respuesta de error de la API (status >= 300) con el
// mensaje `detail` que devuelve FastAPI.
type APIError struct {
	StatusCode int
	Detail     string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("la API respondió %d: %s", e.StatusCode, e.Detail)
}

func (c *Client) do(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	if c.TokenFunc != nil {
		token, err := c.TokenFunc()
		if err != nil {
			return fmt.Errorf("no se pudo obtener un token válido, corré 'asterion login': %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo contactar la API de Asterion (%s): %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var errBody struct {
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(data, &errBody)
		detail := errBody.Detail
		if detail == "" {
			detail = string(data)
		}
		return &APIError{StatusCode: resp.StatusCode, Detail: detail}
	}

	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Session es una sesión de Asterion Cloud: un access token para llamar a
// la API (vía Authorization: Bearer) y un refresh token para renovarlo sin
// volver a pedir un código. Cómo se arman por dentro es un detalle interno
// de la API — este es el único contrato que ve el CLI.
type Session struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// RequestLoginCode le pide a Asterion Cloud que mande un código de acceso
// de un solo uso al email dado (POST /auth/cli/request-code). No requiere
// sesión previa.
func (c *Client) RequestLoginCode(email string) error {
	return c.do(http.MethodPost, "/auth/cli/request-code", map[string]any{"email": email}, nil)
}

// VerifyLoginCode canjea el código recibido por email por una Session
// (POST /auth/cli/verify-code). No requiere sesión previa.
func (c *Client) VerifyLoginCode(email, code string) (Session, error) {
	var out Session
	err := c.do(http.MethodPost, "/auth/cli/verify-code", map[string]any{"email": email, "code": code}, &out)
	return out, err
}

// RefreshSession renueva una Session vencida sin pedir un código nuevo
// (POST /auth/cli/refresh). No requiere sesión previa (usa el refresh token).
func (c *Client) RefreshSession(refreshToken string) (Session, error) {
	var out Session
	err := c.do(http.MethodPost, "/auth/cli/refresh", map[string]any{"refresh_token": refreshToken}, &out)
	return out, err
}

// Me devuelve el perfil del usuario autenticado (GET /auth/me).
func (c *Client) Me() (map[string]any, error) {
	var out map[string]any
	err := c.do(http.MethodGet, "/auth/me", nil, &out)
	return out, err
}

// ListProjects lista los proyectos a los que pertenece el usuario autenticado.
func (c *Client) ListProjects() ([]map[string]any, error) {
	var out []map[string]any
	err := c.do(http.MethodGet, "/projects", nil, &out)
	return out, err
}

// ConnectLocalInstance vincula una instancia creada localmente
// (asterion instances add) a un proyecto de Asterion Cloud, sin
// duplicarla: si externalRef ya está conectado, la API devuelve la misma
// instancia en vez de crear una nueva (POST /projects/{id}/instances/connect-local).
func (c *Client) ConnectLocalInstance(projectID int, payload map[string]any) (map[string]any, error) {
	var out map[string]any
	err := c.do(http.MethodPost, fmt.Sprintf("/projects/%d/instances/connect-local", projectID), payload, &out)
	return out, err
}

// CreateInstanceAPIKey genera una API key nueva para que asterion agent-run
// pueda reportar métricas de una instancia (POST /instances/{id}/api-keys).
// La clave cruda solo se devuelve en esta llamada — no se puede volver a leer.
func (c *Client) CreateInstanceAPIKey(instanceID int, label string) (map[string]any, error) {
	var out map[string]any
	err := c.do(http.MethodPost, fmt.Sprintf("/instances/%d/api-keys", instanceID), map[string]any{"label": label}, &out)
	return out, err
}

// GetAgentStatus consulta el estado del agente de una instancia
// (GET /instances/{id}/agent-status): online/offline/stale/unknown,
// calculado del lado del servidor a partir del último heartbeat.
func (c *Client) GetAgentStatus(instanceID int) (map[string]any, error) {
	var out map[string]any
	err := c.do(http.MethodGet, fmt.Sprintf("/instances/%d/agent-status", instanceID), nil, &out)
	return out, err
}

// RevokeAgent revoca todas las API keys activas de una instancia
// (POST /instances/{id}/agent/revoke) — el paso del lado de Cloud antes de
// desinstalar el servicio local con `asterion cloud uninstall-agent`.
func (c *Client) RevokeAgent(instanceID int) error {
	return c.do(http.MethodPost, fmt.Sprintf("/instances/%d/agent/revoke", instanceID), nil, nil)
}

// ListCloudAccounts lista las cuentas cloud conectadas a un proyecto.
func (c *Client) ListCloudAccounts(projectID int) ([]map[string]any, error) {
	var out []map[string]any
	err := c.do(http.MethodGet, fmt.Sprintf("/projects/%d/cloud-accounts", projectID), nil, &out)
	return out, err
}

// CreateCloudAccount conecta una cuenta cloud nueva a un proyecto. payload
// debe incluir provider_id, alias, region_default y credentials (ver
// app/schemas/cloud_accounts.py del backend para el contrato exacto).
func (c *Client) CreateCloudAccount(projectID int, payload map[string]any) (map[string]any, error) {
	var out map[string]any
	err := c.do(http.MethodPost, fmt.Sprintf("/projects/%d/cloud-accounts", projectID), payload, &out)
	return out, err
}

// ListInstances lista las instancias de un proyecto.
func (c *Client) ListInstances(projectID int) ([]map[string]any, error) {
	var out []map[string]any
	err := c.do(http.MethodGet, fmt.Sprintf("/projects/%d/instances", projectID), nil, &out)
	return out, err
}

// ListProvisioningRequests lista el historial de solicitudes de
// aprovisionamiento de un proyecto.
func (c *Client) ListProvisioningRequests(projectID int) ([]map[string]any, error) {
	var out []map[string]any
	err := c.do(http.MethodGet, fmt.Sprintf("/projects/%d/provisioning-requests", projectID), nil, &out)
	return out, err
}

// CreateProvisioningRequest es el paso "Describir": crea una solicitud en
// estado draft con resourceType (network|instance|managed_database|
// storage_bucket) y su spec, sin validar ni aplicar nada todavía.
func (c *Client) CreateProvisioningRequest(projectID int, resourceType string, spec map[string]any) (map[string]any, error) {
	var out map[string]any
	err := c.do(http.MethodPost, fmt.Sprintf("/projects/%d/provisioning-requests", projectID), map[string]any{
		"resource_type": resourceType,
		"spec":          spec,
	}, &out)
	return out, err
}

// GetProvisioningRequest devuelve el estado actual de una solicitud y, si
// ya fue planificada, sus plan_steps.
func (c *Client) GetProvisioningRequest(requestID int) (map[string]any, error) {
	var out map[string]any
	err := c.do(http.MethodGet, fmt.Sprintf("/provisioning-requests/%d", requestID), nil, &out)
	return out, err
}

// PlanProvisioningRequest es "Validar + Planificar + Estimar": arma el DAG
// de pasos y calcula el costo mensual estimado. No aplica nada todavía.
func (c *Client) PlanProvisioningRequest(requestID int) (map[string]any, error) {
	var out map[string]any
	err := c.do(http.MethodPost, fmt.Sprintf("/provisioning-requests/%d/plan", requestID), nil, &out)
	return out, err
}

// ConfirmProvisioningRequest es "Confirmar": aprueba explícitamente un plan
// ya estimado. Requerido antes de poder aplicarlo.
func (c *Client) ConfirmProvisioningRequest(requestID int) (map[string]any, error) {
	var out map[string]any
	err := c.do(http.MethodPost, fmt.Sprintf("/provisioning-requests/%d/confirm", requestID), nil, &out)
	return out, err
}

// ApplyProvisioningRequest es "Aplicar + Verificar": ejecuta cada paso del
// plan en orden. Requiere que la solicitud esté confirmed.
func (c *Client) ApplyProvisioningRequest(requestID int) (map[string]any, error) {
	var out map[string]any
	err := c.do(http.MethodPost, fmt.Sprintf("/provisioning-requests/%d/apply", requestID), nil, &out)
	return out, err
}
