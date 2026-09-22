package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"asterion-core/internal/sysservices"
)

// servicesCmd responde qué servicios tiene ESTA máquina — systemd en
// Linux, launchd en macOS, el Service Control Manager en Windows (ver
// internal/sysservices, un backend real por SO) — sin sesión, sin
// proyecto, igual que el resto de `asterion local`/`asterion instances`
// (inventario local). Antes vivía anidado bajo `local info services` justo
// para no chocar de nombre con el Service Registry de un proyecto de
// Asterion Cloud (réplicas de un plugin agrupadas por nombre, un concepto
// completamente distinto) — ese quedó movido a `asterion cloud services`
// (ver cloud.go) para que el nombre corto y más obvio, `asterion
// services`, sea el que no pide login.
func servicesCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "services",
		Short: "Servicios de esta máquina (systemd/launchd/SCM según el SO) — sin sesión, sin proyecto, sin costo",
	}
	root.AddCommand(servicesListCmd())
	return root
}

func servicesListCmd() *cobra.Command {
	var asJSON bool
	var onlySystem, onlyDatabase, onlyAPI, onlyOther bool
	cmd := &cobra.Command{
		Use:   "list [nombre-de-servicio]",
		Short: "Servicios de esta máquina — todos, filtrados por categoría, o uno puntual si se nombra",
		Long: "Sin argumento ni flags, lista TODOS los servicios que el gestor de esta máquina\n" +
			"conoce (systemd en Linux, launchd en macOS, el Service Control Manager en Windows —\n" +
			"activos, inactivos o failed, no solo los de Asterion). Con un nombre puntual (ej.\n" +
			"'nginx.service' en Linux, 'com.docker.docker' en macOS, 'MySQL80' en Windows),\n" +
			"consulta solo ese servicio, sin listar los demás.\n\n" +
			"Cada servicio se clasifica automáticamente (ver internal/sysservices.Classify, a\n" +
			"partir del nombre — best-effort, nunca sondea puertos): 'system' (red, ssh, cron,\n" +
			"logs, seguridad — infraestructura del propio SO), 'database' (mysql/postgresql/\n" +
			"redis/etc.), 'api' (nginx/caddy/docker/etc., o cualquier '<algo>-backend'/'-api'/\n" +
			"'-app' con nombre propio) y 'other' para lo que no matchea nada conocido (nunca se\n" +
			"fuerza a una de las otras tres por descarte). --system/--database/--apis/--other\n" +
			"filtran la lista a esas categorías — combinables (ej. --system --database muestra\n" +
			"las dos), no tiene sentido combinarlos con un nombre puntual.\n\n" +
			"Solo lectura, a propósito: no hay 'restart'/'stop'/'start' acá — si ya tenés acceso\n" +
			"a esta terminal, usá el comando nativo del SO (systemctl/launchctl/Restart-Service)\n" +
			"directo, es exactamente lo mismo sin una capa intermedia. El control REMOTO (desde\n" +
			"el dashboard de Asterion Cloud, gateado por permisos y auditado) vive en la pestaña\n" +
			"'Servicios del sistema' de cada instancia — ver 'asterion agent\n" +
			"enable-service-control' para habilitarlo acá (solo Linux por ahora).\n\n" +
			"Si esta máquina ya está asociada a un proyecto de Asterion Cloud (ver 'asterion\n" +
			"cloud connect'), 'asterion cloud services list' muestra en cambio los servicios\n" +
			"REGISTRADOS en ese proyecto (réplicas de un plugin entre varias instancias) — un\n" +
			"concepto de Cloud, distinto de los servicios de esta máquina puntual.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wanted := wantedCategories(onlySystem, onlyDatabase, onlyAPI, onlyOther)

			if len(args) == 1 {
				if len(wanted) > 0 {
					return fmt.Errorf("--system/--database/--apis/--other no tienen sentido junto con un nombre de unidad puntual")
				}
				unit, err := sysservices.Get(args[0])
				if err != nil {
					return err
				}
				if asJSON {
					printJSON(unit)
					return nil
				}
				printSysServiceLine(unit)
				return nil
			}

			units, err := sysservices.List()
			if err != nil {
				return err
			}
			units = filterByCategory(units, wanted)
			if asJSON {
				printJSON(units)
				return nil
			}
			for _, u := range units {
				printSysServiceLine(u)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Salida en JSON, para scripts")
	cmd.Flags().BoolVar(&onlySystem, "system", false, "Solo unidades de infraestructura del sistema operativo (red, ssh, cron, logs, seguridad)")
	cmd.Flags().BoolVar(&onlyDatabase, "database", false, "Solo motores de base de datos (mysql, postgresql, redis, etc.)")
	cmd.Flags().BoolVar(&onlyAPI, "apis", false, "Solo proxies/servidores web/backends de aplicación (nginx, docker, <algo>-backend, etc.)")
	cmd.Flags().BoolVar(&onlyOther, "other", false, "Solo lo que no se pudo clasificar en ninguna categoría conocida")
	return cmd
}

// wantedCategories arma el conjunto de categorías pedidas por flags —
// vacío si no se pasó ninguno, que filterByCategory interpreta como "sin
// filtro" (mostrar todo), no como "no mostrar nada".
func wantedCategories(onlySystem, onlyDatabase, onlyAPI, onlyOther bool) map[sysservices.Category]bool {
	wanted := map[sysservices.Category]bool{}
	if onlySystem {
		wanted[sysservices.CategorySystem] = true
	}
	if onlyDatabase {
		wanted[sysservices.CategoryDatabase] = true
	}
	if onlyAPI {
		wanted[sysservices.CategoryAPI] = true
	}
	if onlyOther {
		wanted[sysservices.CategoryOther] = true
	}
	return wanted
}

func filterByCategory(units []sysservices.Unit, wanted map[sysservices.Category]bool) []sysservices.Unit {
	if len(wanted) == 0 {
		return units
	}
	filtered := make([]sysservices.Unit, 0, len(units))
	for _, u := range units {
		if wanted[u.Category] {
			filtered = append(filtered, u)
		}
	}
	return filtered
}

func printSysServiceLine(u sysservices.Unit) {
	mark := ""
	if u.Protected {
		mark = "  [protegida — Asterion Cloud nunca la controla remotamente]"
	}
	fmt.Printf("%-45s %-10s %-10s %-10s %s%s\n", u.Name, u.Category, u.ActiveState, u.SubState, u.Description, mark)
}
