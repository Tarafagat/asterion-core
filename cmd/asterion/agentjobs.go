package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"asterion-core/internal/osuser"
	"asterion-core/internal/safety"
)

// executeJob corre un PendingJob recibido en la respuesta del heartbeat y
// reporta el resultado de vuelta a Cloud. Nunca reintenta solo ni simula
// éxito — cualquier error (sin privilegios, distro no soportada, ya
// administrado) se manda tal cual en el campo "error" de la respuesta,
// mismo criterio que el resto de Asterion.
func executeJob(apiBaseURL, apiKey string, job PendingJob) {
	if job.JobType != "os_user_manage" {
		postJobResult(apiBaseURL, apiKey, job.ID, false, nil, fmt.Sprintf("tipo de job desconocido: %q", job.JobType))
		return
	}

	// Mismo gate que ya usa 'asterion local user' — un job del Agent nunca
	// se salta la regla de que Apply solo corre si el adapter también
	// declaró Rollback.
	if err := safety.RequireSafeApply(safety.OSUserAdapter{}); err != nil {
		postJobResult(apiBaseURL, apiKey, job.ID, false, nil, err.Error())
		return
	}

	switch job.Action {
	case "create":
		var spec osuser.Spec
		if err := json.Unmarshal(job.Payload, &spec); err != nil {
			postJobResult(apiBaseURL, apiKey, job.ID, false, nil, fmt.Sprintf("payload inválido: %v", err))
			return
		}
		diff, err := osuser.Plan(spec)
		if err != nil {
			postJobResult(apiBaseURL, apiKey, job.ID, false, nil, err.Error())
			return
		}
		result, err := osuser.Apply(diff)
		if err != nil {
			postJobResult(apiBaseURL, apiKey, job.ID, false, nil, err.Error())
			return
		}
		if err := osuser.RecordApply(result.Diff); err != nil {
			// La operación real ya se aplicó — no registrarla localmente
			// (inventario de 'asterion local user list') es una falla
			// menor, se reporta como warning en vez de como fallo del job.
			fmt.Fprintf(os.Stderr, "job %d: aplicado pero no se pudo registrar localmente: %v\n", job.ID, err)
		}
		postJobResult(apiBaseURL, apiKey, job.ID, true, result, "")

	case "remove":
		var diff osuser.Diff
		if err := json.Unmarshal(job.Payload, &diff); err != nil {
			postJobResult(apiBaseURL, apiKey, job.ID, false, nil, fmt.Sprintf("payload inválido: %v", err))
			return
		}
		if err := osuser.Rollback(&diff); err != nil {
			postJobResult(apiBaseURL, apiKey, job.ID, false, nil, err.Error())
			return
		}
		_ = osuser.RemoveManaged(diff.Username) // no-op si el CLI local nunca lo tuvo registrado (job creado del lado agente)
		postJobResult(apiBaseURL, apiKey, job.ID, true, map[string]any{"diff": diff}, "")

	default:
		postJobResult(apiBaseURL, apiKey, job.ID, false, nil, fmt.Sprintf("acción desconocida: %q", job.Action))
	}
}

func postJobResult(apiBaseURL, apiKey string, jobID int64, success bool, result any, errMsg string) {
	payload := map[string]any{"success": success, "result": result}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/agent/jobs/%d/result", apiBaseURL, jobID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "agente (job result):", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Asterion-Api-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agente (job result):", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "agente (job result): la API respondió %d\n", resp.StatusCode)
	}
}
