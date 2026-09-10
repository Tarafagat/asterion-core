package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"asterion-core/internal/dbbackup"
	"asterion-core/internal/osuser"
	"asterion-core/internal/safety"
)

// executeJob corre un PendingJob recibido en la respuesta del heartbeat y
// reporta el resultado de vuelta a Cloud. Nunca reintenta solo ni simula
// éxito — cualquier error (sin privilegios, distro no soportada, binario
// faltante, ya administrado) se manda tal cual en el campo "error" de la
// respuesta, mismo criterio que el resto de Asterion.
func executeJob(apiBaseURL, apiKey string, job PendingJob) {
	switch job.JobType {
	case "os_user_manage":
		executeOSUserJob(apiBaseURL, apiKey, job)
	case "database_discover":
		executeDatabaseDiscoverJob(apiBaseURL, apiKey, job)
	case "database_backup":
		executeDatabaseBackupJob(apiBaseURL, apiKey, job)
	default:
		postJobResult(apiBaseURL, apiKey, job.ID, false, nil, fmt.Sprintf("tipo de job desconocido: %q", job.JobType))
	}
}

func executeOSUserJob(apiBaseURL, apiKey string, job PendingJob) {
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

// executeDatabaseDiscoverJob no necesita ningún payload — Discover es
// credential-less, siempre prueba los mismos puertos default. Nunca falla
// (ver dbbackup.Discover): en el peor caso, "detected" sale vacío.
func executeDatabaseDiscoverJob(apiBaseURL, apiKey string, job PendingJob) {
	detected := dbbackup.Discover(context.Background())
	postJobResult(apiBaseURL, apiKey, job.ID, true, map[string]any{"detected": detected}, "")
}

// agentBackupInit es la respuesta de POST /agent/database-backups (ver
// AgentBackupInitOut en asterion-cloud/backend/app/schemas/agent.py) — la
// contraseña real de la base viaja acá, recién en este momento, nunca
// antes en el payload del job en cola.
type agentBackupInit struct {
	BackupID     int64  `json:"backup_id"`
	Engine       string `json:"engine"`
	Port         int    `json:"port"`
	DatabaseName string `json:"database_name"`
	DBUsername   string `json:"db_username"`
	DBPassword   string `json:"db_password"`
	UploadURL    string `json:"upload_url"`
}

// executeDatabaseBackupJob es el ciclo completo: pide el trabajo a Cloud
// (con la contraseña real recién ahora), hace el dump local, lo sube
// directo a Firebase Storage, y avisa a Cloud cómo salió — tanto por el
// endpoint síncrono (POST .../complete, que es lo que actualiza el
// historial) como por el resultado genérico del job (lo que cierra la
// fila de agent_jobs). Los dos existen porque son cosas distintas: uno es
// "cómo terminó ESTE backup", el otro es "cómo terminó ESTE job" —
// database backup y CLI (ver database.go) comparten el primero, solo el
// job dispatch usa el segundo.
func executeDatabaseBackupJob(apiBaseURL, apiKey string, job PendingJob) {
	var payload struct {
		ConnectionID int    `json:"connection_id"`
		TriggeredBy  string `json:"triggered_by"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		postJobResult(apiBaseURL, apiKey, job.ID, false, nil, fmt.Sprintf("payload inválido: %v", err))
		return
	}

	ctx := context.Background()
	init, err := initAgentBackup(ctx, apiBaseURL, apiKey, payload.ConnectionID, payload.TriggeredBy)
	if err != nil {
		postJobResult(apiBaseURL, apiKey, job.ID, false, nil, fmt.Sprintf("no pude iniciar el backup contra Cloud: %v", err))
		return
	}

	size, dumpErr := runAndUploadDump(ctx, init)
	if err := completeAgentBackup(ctx, apiBaseURL, apiKey, init.BackupID, size, dumpErr); err != nil {
		// El dump puede haber salido bien igual — esto solo avisa que Cloud
		// se quedó sin saberlo, no cambia el resultado del job en sí.
		fmt.Fprintf(os.Stderr, "job %d: no pude avisarle a Cloud que el backup %d terminó: %v\n", job.ID, init.BackupID, err)
	}

	if dumpErr != nil {
		postJobResult(apiBaseURL, apiKey, job.ID, false, nil, dumpErr.Error())
		return
	}
	postJobResult(apiBaseURL, apiKey, job.ID, true, map[string]any{"backup_id": init.BackupID, "size_bytes": size}, "")
}

// initAgentBackup y completeAgentBackup son el mismo par de llamadas que
// usa tanto el job dispatch (arriba) como 'asterion database backup' (ver
// database.go) — un solo lugar para el protocolo síncrono con Cloud, no
// duplicado entre los dos disparadores.
func initAgentBackup(ctx context.Context, apiBaseURL, apiKey string, connectionID int, triggeredBy string) (agentBackupInit, error) {
	body := map[string]any{"connection_id": connectionID, "triggered_by": triggeredBy}
	respBody, err := agentAPIRequest(ctx, apiBaseURL, apiKey, http.MethodPost, "/agent/database-backups", body)
	if err != nil {
		return agentBackupInit{}, err
	}
	var init agentBackupInit
	if err := json.Unmarshal(respBody, &init); err != nil {
		return agentBackupInit{}, fmt.Errorf("respuesta inválida: %w", err)
	}
	return init, nil
}

func completeAgentBackup(ctx context.Context, apiBaseURL, apiKey string, backupID int64, sizeBytes int64, dumpErr error) error {
	body := map[string]any{"success": dumpErr == nil}
	if dumpErr != nil {
		body["error"] = dumpErr.Error()
	} else {
		body["size_bytes"] = sizeBytes
	}
	path := fmt.Sprintf("/agent/database-backups/%d/complete", backupID)
	_, err := agentAPIRequest(ctx, apiBaseURL, apiKey, http.MethodPost, path, body)
	return err
}

// runAndUploadDump corre el dump local y lo sube — dueño del archivo
// temporal de punta a punta, lo borra apenas termina de subirlo (o al
// fallar), nunca lo deja tirado en /tmp.
func runAndUploadDump(ctx context.Context, init agentBackupInit) (int64, error) {
	path, err := dbbackup.Dump(ctx, init.Engine, init.Port, init.DatabaseName, init.DBUsername, init.DBPassword)
	if err != nil {
		return 0, err
	}
	defer os.Remove(path)

	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if err := dbbackup.Upload(ctx, init.UploadURL, path); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func postJobResult(apiBaseURL, apiKey string, jobID int64, success bool, result any, errMsg string) {
	payload := map[string]any{"success": success, "result": result}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	path := fmt.Sprintf("/agent/jobs/%d/result", jobID)
	if _, err := agentAPIRequest(context.Background(), apiBaseURL, apiKey, http.MethodPost, path, payload); err != nil {
		fmt.Fprintln(os.Stderr, "agente (job result):", err)
	}
}
