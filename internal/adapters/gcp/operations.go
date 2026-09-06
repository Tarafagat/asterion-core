package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// zoneOperationPollInterval/zoneOperationMaxAttempts: instances.insert
// normalmente termina en 10-30s, pero se deja margen generoso (5 minutos)
// para no fallar en falso por una zona momentáneamente lenta — GCP no
// documenta un límite superior real para esto. zoneOperationPollInterval
// es var (no const), mismo criterio que computeAPIBaseURL en adapter.go
// — los tests la bajan a microsegundos para no esperar de verdad.
var zoneOperationPollInterval = 2 * time.Second

// zoneOperationMaxAttempts también es var por el mismo motivo — un test
// de timeout la baja para no tener que esperar 150 intentos de verdad.
var zoneOperationMaxAttempts = 150 // 150 * zoneOperationPollInterval = 5 minutos en producción

// zoneOperationError es el shape real del campo "error" de una Operation
// de GCP cuando la operación termina DONE pero falló (ej. cuota excedida,
// imagen inexistente, tipo de máquina inválido en esa zona) — DONE no
// significa "salió bien", solo "la API terminó de procesarla".
type zoneOperationError struct {
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// waitForZoneOperation sondea una Operation zonal de Compute Engine hasta
// que termina (status == "DONE"), devolviendo un error claro si la
// operación falló (no solo si se agotó el tiempo de espera). Respeta la
// cancelación de ctx — si el caller cancela, la espera corta ahí mismo en
// vez de seguir sondeando una operación que ya no le importa a nadie.
func waitForZoneOperation(ctx context.Context, token, projectID, zone, operationName string) error {
	requestURL := fmt.Sprintf(
		"%s/projects/%s/zones/%s/operations/%s",
		computeAPIBaseURL, projectID, zone, operationName,
	)

	for attempt := 0; attempt < zoneOperationMaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("gcp: se canceló la espera de la operación %s: %w", operationName, ctx.Err())
			case <-time.After(zoneOperationPollInterval):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("gcp: no se pudo conectar a Compute Engine para revisar la operación %s: %w", operationName, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("gcp: Compute Engine respondió %d al revisar la operación %s: %s", resp.StatusCode, operationName, strings.TrimSpace(string(body)))
		}

		var parsed struct {
			Status string              `json:"status"`
			Error  *zoneOperationError `json:"error"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return fmt.Errorf("gcp: no pude interpretar el estado de la operación %s: %w", operationName, err)
		}

		if parsed.Status != "DONE" {
			continue
		}
		if parsed.Error != nil && len(parsed.Error.Errors) > 0 {
			msgs := make([]string, len(parsed.Error.Errors))
			for i, e := range parsed.Error.Errors {
				msgs[i] = fmt.Sprintf("%s: %s", e.Code, e.Message)
			}
			return fmt.Errorf("gcp: la operación %s terminó con error: %s", operationName, strings.Join(msgs, "; "))
		}
		return nil
	}

	return fmt.Errorf("gcp: la operación %s no terminó después de %s", operationName, zoneOperationPollInterval*time.Duration(zoneOperationMaxAttempts))
}
