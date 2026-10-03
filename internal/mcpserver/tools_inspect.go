package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"asterion-core/internal/doctor"
	"asterion-core/internal/importer"
	"asterion-core/internal/plugins"
)

// toolInspectProject es de solo lectura, siempre — no escribe ningún
// archivo ni crea nada, a diferencia de 'asterion import' (que sí escribe
// un app.asterion) y de 'asterion doctor' (que no escribe nada, pero esta
// tool reusa su lógica igual). Es la forma en que un agente pregunta "¿qué
// hay acá?" antes de decidir qué más pedir.
//
// Dos casos, según lo que encuentre en 'path':
//   - ya es un plugin de Asterion (tiene plugin.yaml): devuelve lo que
//     declara (servicios, config, permisos) y, si además está instalado y
//     corriendo, el mismo reporte de salud que da 'asterion doctor'.
//   - es un proyecto cualquiera, sin plugin.yaml: corre el mismo escaneo
//     de 'asterion import' (Scan, no Generate — nunca escribe nada acá) y
//     devuelve qué detectó: lenguaje, comando de arranque, servicios
//     externos, variables de entorno.
func toolInspectProject() Tool {
	return Tool{
		Name:        "inspect_project",
		Description: "Inspecciona un directorio (default: el actual) de solo lectura — detecta si ya es un plugin de Asterion (y su salud, si está instalado) o, si es un proyecto cualquiera, qué lenguaje/arranque/servicios externos tiene.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Directorio a inspeccionar (default: \".\")."},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) Result {
			path := argString(args, "path", ".")
			if _, err := os.Stat(path); err != nil {
				return fail(fmt.Sprintf("%q no existe o no se puede leer: %v", path, err))
			}

			if _, err := os.Stat(path + "/plugin.yaml"); err == nil {
				return inspectAsPlugin(ctx, path)
			}
			return inspectAsRawProject(path)
		},
	}
}

func inspectAsPlugin(ctx context.Context, path string) Result {
	manifest, err := plugins.ValidateManifestDir(path)
	if err != nil {
		return fail(fmt.Sprintf("hay un plugin.yaml en %q pero no pasa la validación del contrato: %v", path, err))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Es un plugin de Asterion: %q (v%s)\n", manifest.Name, manifest.Version)
	if manifest.Language != nil {
		fmt.Fprintf(&b, "lenguaje: %s %s\n", manifest.Language.Name, manifest.Language.Version)
	}
	fmt.Fprintf(&b, "arranque: %s\n", manifest.Start.Command)
	if len(manifest.ConfigSchema) > 0 {
		fmt.Fprintf(&b, "config declarada: %d campo(s)\n", len(manifest.ConfigSchema))
	}
	if len(manifest.Services) > 0 {
		var names []string
		for _, s := range manifest.Services {
			names = append(names, fmt.Sprintf("%s (%s)", s.Name, s.Kind))
		}
		fmt.Fprintf(&b, "servicios externos declarados: %s\n", strings.Join(names, ", "))
	}
	if manifest.Permissions != nil {
		fmt.Fprintf(&b, "permissions.network: %v\npermissions.filesystem: %v\n", manifest.Permissions.Network, manifest.Permissions.Filesystem)
	}

	// ¿Está además instalado y registrado? Si sí, se suma el mismo
	// diagnóstico que 'asterion doctor' — pero solo si de verdad es este
	// mismo directorio, no un plugin instalado que coincide de nombre.
	installed, err := findInstalledAt(path)
	if err == nil {
		fmt.Fprintf(&b, "\n--- instalado como %q — diagnóstico ---\n", installed.Name)
		report := doctor.Run(ctx, installed)
		for _, c := range report.Checks {
			fmt.Fprintf(&b, "[%s] %s %s: %s\n", c.Section, c.Severity.Marker(), c.Name, c.Detail)
		}
	} else {
		b.WriteString("\nNo está instalado en este Asterion (ver 'asterion plugin install . --link' para probarlo).\n")
	}
	return ok(b.String())
}

// findInstalledAt busca, entre los plugins instalados, uno cuyo Dir
// coincida con path — no alcanza con el nombre del manifiesto: un plugin
// con ese mismo nombre podría estar instalado desde OTRA carpeta.
func findInstalledAt(path string) (plugins.Installed, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return plugins.Installed{}, err
	}
	all, err := plugins.List()
	if err != nil {
		return plugins.Installed{}, err
	}
	for _, p := range all {
		pDir, err1 := filepath.Abs(p.Dir)
		if err1 == nil && pDir == abs {
			return p, nil
		}
	}
	return plugins.Installed{}, fmt.Errorf("no está instalado")
}

func inspectAsRawProject(path string) Result {
	scan, err := importer.Run(path)
	if err != nil {
		return fail(err.Error())
	}

	var b strings.Builder
	fmt.Fprintf(&b, "No es un plugin de Asterion todavía (sin plugin.yaml). Lo que detecté:\n\n")
	fmt.Fprintf(&b, "nombre sugerido: %s\n", scan.Name.Value)
	if scan.Language.Value != "" {
		fmt.Fprintf(&b, "lenguaje: %s %s (%s)\n", scan.Language.Value, scan.LangVer.Value, originStr(scan.Language))
	} else {
		fmt.Fprintf(&b, "lenguaje: no detectado\n")
	}
	if scan.Start.Value != "" {
		fmt.Fprintf(&b, "arranque: %q (%s)\n", scan.Start.Value, originStr(scan.Start))
	}
	fmt.Fprintf(&b, "puerto: %s (%s)\n", scan.Port.Value, originStr(scan.Port))
	fmt.Fprintf(&b, "variables de config detectadas: %d\n", len(scan.Configs))
	for _, svc := range scan.Services {
		fmt.Fprintf(&b, "servicio externo detectado: %s (%s)\n", svc.Name, svc.Kind)
	}
	for _, w := range scan.Warnings {
		fmt.Fprintf(&b, "⚠ %s\n", w)
	}
	fmt.Fprintf(&b, "\nPara convertirlo en un plugin de Asterion: 'asterion import %s' genera un app.asterion de partida.\n", path)
	return ok(b.String())
}

func originStr(f importer.Finding) string {
	if f.Confidence == importer.Declared {
		return "detectado en " + f.Source
	}
	return "adivinado: " + f.Source
}
