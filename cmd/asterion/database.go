package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"asterion-core/internal/cliconfig"
	"asterion-core/internal/localstore"
)

// databaseCmd agrupa las acciones que el agente de ESTA instancia puede
// hacer sobre bases de datos ya registradas en Asterion Cloud — hoy solo
// backup, disparado a mano en vez de esperar al próximo heartbeat.
func databaseCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "database",
		Short: "Backups de bases de datos manejados por el agente de esta instancia",
	}
	root.AddCommand(databaseBackupCmd())
	return root
}

func databaseBackupCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "backup <alias-o-id-de-conexion> [nombre-local-de-instancia]",
		Short: "Dispara ahora mismo el backup de una conexión ya registrada en Asterion Cloud",
		Long: "No espera al próximo heartbeat (hasta ~30s) — hace el dump y lo sube ya\n" +
			"mismo, usando la misma clave de agente que 'asterion agent-run' ya tiene\n" +
			"guardada para esta máquina.\n\n" +
			"Opera sobre una conexión que Cloud YA tiene registrada (creála primero\n" +
			"desde el panel del proyecto) — deliberadamente NO acepta usuario/\n" +
			"contraseña como argumentos: una contraseña cruda en la línea de comandos\n" +
			"queda en el historial de bash y es visible para cualquier otro usuario de\n" +
			"la máquina vía 'ps aux' mientras el proceso corre. La contraseña real la\n" +
			"tiene Cloud, cifrada, y viaja recién en el momento de ejecutar.\n\n" +
			"Sin el segundo argumento, identifica sola la instancia que representa A\n" +
			"ESTA MÁQUINA (mismo criterio que 'asterion agent status/restart').",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var instance localstore.Instance
			var err error
			if len(args) == 2 {
				instance, err = localstore.Get(args[1])
			} else {
				instance, err = resolveSelfInstance()
			}
			if err != nil {
				return err
			}

			apiKey, err := loadAgentKey(instance.ID)
			if err != nil {
				return err
			}
			cfg, err := cliconfig.Load()
			if err != nil {
				return err
			}

			ctx := context.Background()
			connectionID, err := resolveConnectionID(ctx, cfg.APIBaseURL, apiKey, args[0])
			if err != nil {
				return err
			}

			if !asJSON {
				fmt.Printf("Iniciando backup de %q…\n", args[0])
			}
			init, err := initAgentBackup(ctx, cfg.APIBaseURL, apiKey, connectionID, "cli")
			if err != nil {
				return fmt.Errorf("no pude iniciar el backup contra Cloud: %w", err)
			}

			size, dumpErr := runAndUploadDump(ctx, init)
			if completeErr := completeAgentBackup(ctx, cfg.APIBaseURL, apiKey, init.BackupID, size, dumpErr); completeErr != nil {
				// El dump puede haber salido bien igual — esto solo avisa que
				// Cloud se quedó sin saberlo, no cambia el resultado de este
				// comando en sí (ver mismo criterio en executeDatabaseBackupJob).
				fmt.Fprintf(cmd.ErrOrStderr(), "aviso: no pude avisarle a Cloud que el backup terminó: %v\n", completeErr)
			}

			if dumpErr != nil {
				if asJSON {
					printJSON(map[string]any{"backup_id": init.BackupID, "success": false, "error": dumpErr.Error()})
					return nil
				}
				return fmt.Errorf("el backup falló: %w", dumpErr)
			}
			if asJSON {
				printJSON(map[string]any{"backup_id": init.BackupID, "success": true, "size_bytes": size})
				return nil
			}
			fmt.Printf("✓ Backup #%d completo (%d bytes)\n", init.BackupID, size)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Salida en JSON, para scripts")
	return cmd
}

// resolveConnectionID acepta un alias o un id numérico de conexión —
// consulta GET /agent/database-connections, que devuelve solo las
// conexiones de ESTA instancia (misma "asociación mínima" del resto del
// protocolo). Si no matchea nada, el error lista los alias disponibles en
// vez de solo decir "no encontrado" a secas.
func resolveConnectionID(ctx context.Context, apiBaseURL, apiKey, aliasOrID string) (int, error) {
	respBody, err := agentAPIRequest(ctx, apiBaseURL, apiKey, http.MethodGet, "/agent/database-connections", nil)
	if err != nil {
		return 0, fmt.Errorf("no pude listar las conexiones de esta instancia: %w", err)
	}
	var connections []struct {
		ID    int    `json:"id"`
		Alias string `json:"alias"`
	}
	if err := json.Unmarshal(respBody, &connections); err != nil {
		return 0, fmt.Errorf("respuesta inválida al listar conexiones: %w", err)
	}

	if id, convErr := strconv.Atoi(aliasOrID); convErr == nil {
		for _, c := range connections {
			if c.ID == id {
				return c.ID, nil
			}
		}
	}
	for _, c := range connections {
		if c.Alias == aliasOrID {
			return c.ID, nil
		}
	}

	if len(connections) == 0 {
		return 0, fmt.Errorf("esta instancia no tiene ninguna conexión de base de datos registrada en Asterion Cloud todavía")
	}
	aliases := make([]string, len(connections))
	for i, c := range connections {
		aliases[i] = c.Alias
	}
	return 0, fmt.Errorf("no encontré una conexión %q — conexiones disponibles: %s", aliasOrID, strings.Join(aliases, ", "))
}
