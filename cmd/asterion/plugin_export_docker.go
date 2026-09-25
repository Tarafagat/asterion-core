package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"asterion-core/internal/plugins"
)

// writeDockerfile genera un Dockerfile de referencia — punto de partida
// para 'docker build', nunca ejecutado por Asterion (ver doc comment de
// pluginExportCmd). Siempre recompila desde el código fuente copiado (no
// copia el binario Go ya compilado del host, ni el venv de Python — ver
// copyPluginTree): un binario/venv del host podría no matchear la
// arquitectura o el layout de rutas del contenedor.
func writeDockerfile(path string, installed plugins.Installed, port int, includeFrontend, isPython bool) error {
	var b strings.Builder
	b.WriteString("# Generado por 'asterion plugin export' — punto de partida, ajustalo si hace\n")
	b.WriteString("# falta. Asterion nunca corre 'docker build' por su cuenta; este archivo es\n")
	b.WriteString("# para que lo uses vos cuando quieras una imagen real.\n#\n")
	b.WriteString("# .env_asterion_produced NO se copia a la imagen a propósito (tiene secretos\n")
	b.WriteString("# reales) — pasalo en runtime:\n")
	fmt.Fprintf(&b, "#   docker build -t %s .\n", installed.Name)
	fmt.Fprintf(&b, "#   docker run --env-file .env_asterion_produced -p %d:%d %s\n#\n", port, port, installed.Name)
	b.WriteString("# ⚠️  Si el código de este plugin escucha en 127.0.0.1 (la convención de\n")
	b.WriteString("# desarrollo local de Asterion — así asume que todo corre en el mismo host),\n")
	b.WriteString("# NO va a responder a través de un puerto publicado de Docker (confirmado en\n")
	b.WriteString("# vivo: el port mapping de Docker apunta a la interfaz real del contenedor,\n")
	b.WriteString("# nunca a su loopback). Para que 'docker run -p' funcione, el código del\n")
	b.WriteString("# plugin tiene que escuchar en 0.0.0.0 — eso lo controla el código del\n")
	b.WriteString("# plugin, no este Dockerfile.\n\n")

	if includeFrontend {
		b.WriteString("FROM node:20-slim AS frontend-build\n")
		b.WriteString("WORKDIR /frontend\n")
		b.WriteString("COPY frontend/package.json ./\n")
		b.WriteString("COPY frontend/pnpm-lock.yaml* ./\n")
		b.WriteString("RUN corepack enable && pnpm install\n")
		b.WriteString("COPY frontend/ .\n")
		b.WriteString("RUN pnpm build\n\n")
	}

	if isPython {
		writePythonDockerStages(&b, installed, includeFrontend)
	} else {
		writeGoDockerStages(&b, installed, includeFrontend)
	}

	fmt.Fprintf(&b, "\nEXPOSE %d\n", port)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeGoDockerStages(b *strings.Builder, installed plugins.Installed, includeFrontend bool) {
	outputName := strings.TrimPrefix(installed.Manifest.Start.Command, "./")
	if outputName == "" {
		outputName = installed.Name
	}

	b.WriteString("FROM golang:1.25 AS build\n")
	b.WriteString("WORKDIR /src\n")
	b.WriteString("COPY . .\n")
	fmt.Fprintf(b, "RUN go build -o /out/%s .\n\n", outputName)

	b.WriteString("FROM debian:bookworm-slim\n")
	b.WriteString("WORKDIR /app\n")
	fmt.Fprintf(b, "COPY --from=build /out/%s ./%s\n", outputName, outputName)
	if includeFrontend {
		b.WriteString("COPY --from=frontend-build /frontend/dist ./frontend/dist\n")
	}
	fmt.Fprintf(b, "CMD [\"./%s\"]\n", outputName)
}

func writePythonDockerStages(b *strings.Builder, installed plugins.Installed, includeFrontend bool) {
	pyVersion := "3.11"
	if installed.Manifest.Language != nil && installed.Manifest.Language.Version != "" {
		pyVersion = installed.Manifest.Language.Version
	}

	venvBinDir := filepath.Dir(installed.Manifest.Start.Command)
	venvDir := filepath.Dir(venvBinDir)
	requirementsPath := filepath.Join(filepath.Dir(venvDir), "requirements.txt")

	fmt.Fprintf(b, "FROM python:%s-slim\n", pyVersion)
	b.WriteString("WORKDIR /app\n")
	fmt.Fprintf(b, "COPY %s %s\n", requirementsPath, requirementsPath)
	fmt.Fprintf(b, "RUN pip install --no-cache-dir -r %s\n", requirementsPath)
	b.WriteString("COPY . .\n")
	if includeFrontend {
		b.WriteString("COPY --from=frontend-build /frontend/dist ./frontend/dist\n")
	}

	// Start.Args ya son rutas relativas al root del plugin (ej.
	// "backend/run.py") — siguen siendo válidas sin el venv, apuntando
	// directo al python3 de la imagen en vez de backend/venv/bin/python.
	args := append([]string{"python3"}, installed.Manifest.Start.Args...)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	fmt.Fprintf(b, "CMD [%s]\n", strings.Join(quoted, ", "))
}

// writeExportReadme genera una guía corta de qué hay en la carpeta y
// cómo correrla — sin asumir que quien la lea tiene el CLI de asterion
// instalado (todo el punto de exportar es no necesitarlo).
func writeExportReadme(path string, installed plugins.Installed, includeFrontend, isPython bool) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — export de Asterion\n\n", installed.Name)
	b.WriteString("Generado por `asterion plugin export` — autocontenido, no necesita el CLI de\n")
	b.WriteString("asterion ni su estado local para correr.\n\n")

	b.WriteString("## ⚠️ `.env_asterion_produced`\n\n")
	b.WriteString("Contiene los secretos reales configurados para este plugin. Permisos 0600,\n")
	b.WriteString("excluido en `.gitignore` — **nunca lo commitees**. Si se filtra, rotá\n")
	b.WriteString("cualquier secreto que tenga adentro.\n\n")

	b.WriteString("## Correrlo directo (sin Docker)\n\n")
	if isPython {
		b.WriteString("Este es un plugin Python — su venv NO se copió (los venv no son portables\n")
		b.WriteString("entre máquinas, referencian rutas absolutas del intérprete original).\n")
		b.WriteString("Recreálo acá antes de arrancar:\n\n")
		b.WriteString("```sh\n")
		venvBinDir := filepath.Dir(installed.Manifest.Start.Command)
		venvDir := filepath.Dir(venvBinDir)
		requirementsPath := filepath.Join(filepath.Dir(venvDir), "requirements.txt")
		fmt.Fprintf(&b, "python3 -m venv %s\n", venvDir)
		fmt.Fprintf(&b, "%s/pip install -r %s\n", venvBinDir, requirementsPath)
		b.WriteString("set -a; source .env_asterion_produced; set +a\n")
		fmt.Fprintf(&b, "%s %s\n", installed.Manifest.Start.Command, strings.Join(installed.Manifest.Start.Args, " "))
		b.WriteString("```\n\n")
	} else {
		b.WriteString("```sh\n")
		b.WriteString("set -a; source .env_asterion_produced; set +a\n")
		outputName := strings.TrimPrefix(installed.Manifest.Start.Command, "./")
		fmt.Fprintf(&b, "./%s\n", outputName)
		b.WriteString("```\n\n")
	}

	b.WriteString("## Con Docker\n\n")
	b.WriteString("```sh\n")
	fmt.Fprintf(&b, "docker build -t %s .\n", installed.Name)
	fmt.Fprintf(&b, "docker run --env-file .env_asterion_produced -p 8080:8080 %s\n", installed.Name)
	b.WriteString("```\n\n")
	b.WriteString("El `Dockerfile` es un punto de partida generado automáticamente — revisalo\n")
	b.WriteString("antes de confiar en él para producción.\n\n")
	b.WriteString("**⚠️ Si `docker run -p` no responde**: confirmado en vivo — si el código de\n")
	b.WriteString("este plugin escucha en `127.0.0.1` (la convención de desarrollo local de\n")
	b.WriteString("Asterion), el puerto publicado de Docker no llega ahí (apunta a la interfaz\n")
	b.WriteString("real del contenedor, nunca a su loopback). Para correr en Docker, el código\n")
	b.WriteString("tiene que escuchar en `0.0.0.0` — un cambio en el propio plugin, no algo que\n")
	b.WriteString("este export pueda resolver por vos.\n\n")

	if includeFrontend {
		b.WriteString("## Frontend\n\n")
		b.WriteString("`frontend/.env.production` trae SOLO los valores de config no marcados\n")
		b.WriteString("secretos — nunca los reales de `.env_asterion_produced`. Según tu\n")
		b.WriteString("framework (Vite/Next/CRA), puede hacer falta ajustar el prefijo de env\n")
		b.WriteString("vars que tu propio build config expone al navegador para que las levante.\n")
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}
