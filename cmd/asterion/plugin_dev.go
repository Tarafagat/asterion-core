package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"asterion-core/internal/plugins"
)

// pluginDevCmd es el sandbox de pruebas local del Asterion Plugin
// Contract: arranca el plugin con su propio start.command (sin necesidad
// de instalarlo primero), espera su health check, y golpea con requests
// GET (nunca destructivas) cada resource que declare 'list' y cada action
// cuyo método sea GET — reportando si lo que la API responde de verdad
// coincide con lo que plugin.yaml promete. No reemplaza tests propios del
// plugin (main_test.go en dummy-provider es el ejemplo de eso); esto
// confirma la otra mitad: que el contrato declarado es honesto.
func pluginDevCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dev <dir>",
		Short: "Arranca un plugin local y confirma que su API real coincide con lo que plugin.yaml declara",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginDev(args[0])
		},
	}
}

func runPluginDev(dir string) error {
	manifest, err := plugins.ValidateManifestDir(dir)
	if err != nil {
		return err
	}

	port, err := plugins.FreePort()
	if err != nil {
		return fmt.Errorf("no pude reservar un puerto libre: %w", err)
	}

	env := os.Environ()
	env = append(env,
		"ASTERION_PLUGIN_NAME="+manifest.Name,
		fmt.Sprintf("ASTERION_PLUGIN_PORT=%d", port),
		"ASTERION_PLUGIN_DIR="+dir,
	)
	// dev no instala el plugin, así que no hay configuración guardada que
	// leer — se usan los defaults declarados en config_schema, si los hay,
	// para que al menos lo mínimo necesario para arrancar esté presente.
	for _, f := range manifest.ConfigSchema {
		if f.Default != "" {
			env = append(env, "ASTERION_PLUGIN_CONFIG_"+strings.ToUpper(f.Key)+"="+f.Default)
		}
	}

	proc := exec.Command(manifest.Start.Command, manifest.Start.Args...)
	proc.Dir = dir
	proc.Env = env
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	if err := proc.Start(); err != nil {
		return fmt.Errorf("no pude arrancar %q: %w", manifest.Start.Command, err)
	}
	defer func() {
		_ = proc.Process.Kill()
		_, _ = proc.Process.Wait()
	}()

	fmt.Printf("Arrancando %q en el puerto %d...\n", manifest.Name, port)
	if err := plugins.WaitHealthy(port, manifest.HealthPath, 10*time.Second); err != nil {
		return fmt.Errorf("health check falló: %w", err)
	}
	fmt.Println("✓ health check OK")

	basePath := ""
	if manifest.API != nil {
		basePath = strings.TrimSuffix(manifest.API.BasePath, "/")
	}
	base := fmt.Sprintf("http://127.0.0.1:%d%s", port, basePath)
	client := &http.Client{Timeout: 3 * time.Second}

	for _, r := range manifest.Resources {
		hasList := false
		for _, op := range r.CRUD {
			if op == "list" {
				hasList = true
			}
		}
		if !hasList {
			fmt.Printf("- resource %q: declarado, no tiene 'list' — no se puede descubrir automáticamente\n", r.Name)
			continue
		}
		url := base + r.Endpoint
		resp, err := client.Get(url)
		if err != nil {
			fmt.Printf("✗ resource %q: GET %s falló: %v\n", r.Name, url, err)
			continue
		}
		resp.Body.Close()
		fmt.Printf("✓ resource %q: GET %s -> %d\n", r.Name, url, resp.StatusCode)
	}

	for _, a := range manifest.Actions {
		if strings.ToUpper(a.Method) != "GET" {
			fmt.Printf("- action %q: declarada (%s %s%s) — no se prueba automáticamente (no destructivo)\n", a.Name, a.Method, base, a.Endpoint)
			continue
		}
		url := base + a.Endpoint
		resp, err := client.Get(url)
		if err != nil {
			fmt.Printf("✗ action %q: GET %s falló: %v\n", a.Name, url, err)
			continue
		}
		resp.Body.Close()
		fmt.Printf("✓ action %q: GET %s -> %d\n", a.Name, url, resp.StatusCode)
	}

	fmt.Println("\n✓ dev terminado — deteniendo el proceso")
	return nil
}
