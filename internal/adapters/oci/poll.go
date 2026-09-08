package oci

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"asterion-core/internal/adapters"
)

// instanceOperationPollInterval/instanceOperationMaxAttempts son var (no
// const) por lo mismo que zoneOperationPollInterval en
// internal/adapters/gcp/operations.go: los tests las bajan a milisegundos
// y unos pocos intentos, restaurando el valor original con t.Cleanup.
var instanceOperationPollInterval = 5 * time.Second
var instanceOperationMaxAttempts = 100 // 100 * 5s ≈ 8 minutos en producción — lanzar una VM en OCI suele tardar más que en GCP

// waitForRunning consulta GetInstance hasta que lifecycleState sea RUNNING
// o un estado terminal de fallo (TERMINATED/TERMINATING) — LaunchInstance
// devuelve la instancia recién creada en PROVISIONING, nunca lista al
// toque (mismo motivo que GCP necesita esperar su Operation asíncrona,
// aunque acá no hay un recurso "Operation" aparte: se repregunta la
// instancia misma).
func waitForRunning(ctx context.Context, region, instanceID string, creds apiKeyCredentials) (adapters.InstanceResult, error) {
	getURL := iaasBaseURL(region) + "/20160918/instances/" + instanceID

	for attempt := 0; attempt < instanceOperationMaxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return adapters.InstanceResult{}, ctx.Err()
		default:
		}

		body, status, _, err := doSigned(ctx, http.MethodGet, getURL, creds, nil)
		if err != nil {
			return adapters.InstanceResult{}, err
		}
		if status != http.StatusOK {
			return adapters.InstanceResult{}, fmt.Errorf("oci: Compute API respondió %d al leer la instancia recién creada: %s", status, strings.TrimSpace(string(body)))
		}

		var parsed struct {
			ID             string `json:"id"`
			LifecycleState string `json:"lifecycleState"`
		}
		if err := decodeJSON(body, &parsed); err != nil {
			return adapters.InstanceResult{}, err
		}

		state := strings.ToUpper(parsed.LifecycleState)
		switch state {
		case "RUNNING":
			return adapters.InstanceResult{ExternalID: parsed.ID, Status: strings.ToLower(state)}, nil
		case "TERMINATED", "TERMINATING":
			return adapters.InstanceResult{}, fmt.Errorf("oci: la instancia terminó en estado %s en vez de llegar a RUNNING (posible falta de capacidad del shape en este availability domain)", state)
		}

		select {
		case <-ctx.Done():
			return adapters.InstanceResult{}, ctx.Err()
		case <-time.After(instanceOperationPollInterval):
		}
	}
	return adapters.InstanceResult{}, fmt.Errorf("oci: la instancia no llegó a RUNNING después de %d intentos", instanceOperationMaxAttempts)
}
