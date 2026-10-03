// Package importer inspecciona un proyecto EXISTENTE — uno que no sabe
// nada de Asterion, con su package.json/requirements.txt/Dockerfile/
// docker-compose.yml de siempre— y arma un borrador de app.asterion a
// partir de lo que encuentra.
//
// La idea completa es la puerta de entrada: en vez de "reescribí tu
// infraestructura para usar Asterion", el camino es "corré esto acá
// adentro y mirá qué detectó". Lo que no se pudo inferir con confianza
// real se marca así, explícito, nunca se completa adivinando en silencio
// — un app.asterion con un start.command inventado que compila pero no
// arranca es peor que uno que para y dice qué completar a mano.
package importer

import (
	"fmt"
	"os"
	"path/filepath"
)

// Confidence dice qué tan seguro está un dato detectado. Se imprime en el
// reporte y, para lo que es obligatorio en el contrato (start.command),
// decide si se emite un placeholder bien visible en vez de una adivinanza
// que parezca un dato real.
type Confidence int

const (
	// Guessed: una inferencia razonable pero no confirmada por una fuente
	// que lo declare explícitamente (ej. "hay un main.go, probablemente el
	// binario se llama como el módulo").
	Guessed Confidence = iota
	// Declared: una fuente que lo dice directamente (package.json
	// "scripts.start", Dockerfile CMD, .env.example con la clave puesta,
	// docker-compose "image: postgres:16").
	Declared
)

// Source identifica de qué archivo salió un dato — para que el reporte
// pueda decir "puerto: 8080 (de .env.example)" en vez de solo "puerto: 8080".
type Finding struct {
	Value      string
	Confidence Confidence
	Source     string // ruta relativa al archivo de donde salió
}

// ConfigKey es un Contract.config(...) candidato.
type ConfigKey struct {
	Key      string
	Type     string // string | number | bool | secret
	Required bool
	Default  string
	Source   string
}

// ServiceCandidate es un Contract.service(...) candidato.
type ServiceCandidate struct {
	Name         string
	Kind         string // postgres | mysql | mariadb | redis
	Version      string
	Database     string
	User         string
	MapsHost     string
	MapsPort     string
	MapsUser     string
	MapsPassword string
	MapsDatabase string
	MapsURL      string
	Confidence   Confidence
	Source       string
}

// Scan es todo lo que se pudo averiguar de un directorio.
type Scan struct {
	Dir string

	Name    Finding // nombre del plugin
	Version Finding // SIEMPRE tiene un valor (default "0.1.0") — Contract.define lo exige

	Language Finding // "go" | "python" | "node" | "rust" | "" (no detectado)
	LangVer  Finding

	Start Finding // comando (ya resuelto en forma "comando arg1 arg2")
	Port  Finding // "0" cuando no se detectó — es un valor válido y correcto, no una falla

	Configs  []ConfigKey
	Services []ServiceCandidate

	// FilesSeen son los archivos de manifiesto que el scan encontró y usó
	// — lo que imprime 'asterion import' para decir en qué se basó.
	FilesSeen []string

	// Warnings son los huecos reales: nada que inferir, o señales
	// contradictorias (dos lenguajes de backend a la vez, por ejemplo).
	Warnings []string
}

func (s *Scan) sawFile(path string) {
	for _, seen := range s.FilesSeen {
		if seen == path {
			return // más de un detector lee el mismo archivo — se cuenta una vez
		}
	}
	if _, err := os.Stat(filepath.Join(s.Dir, path)); err == nil {
		s.FilesSeen = append(s.FilesSeen, path)
	}
}

func (s *Scan) warn(format string, args ...any) {
	s.Warnings = append(s.Warnings, fmt.Sprintf(format, args...))
}

// Run escanea dir y devuelve todo lo detectado. No escribe nada: separar
// detectar de generar deja cada paso probable por separado, y permite que
// el reporte de consola y el archivo final se construyan de la misma
// fuente de verdad.
func Run(dir string) (*Scan, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	s := &Scan{Dir: abs}

	s.Name = detectName(s)
	s.Version = Finding{Value: "0.1.0", Confidence: Guessed, Source: "no se detectó ninguna versión declarada"}

	detectLanguageAndStart(s)
	detectPort(s)
	detectConfig(s)
	detectServices(s)

	if s.Start.Value == "" {
		s.warn("no pude inferir un comando de arranque — completá Contract.start(command=...) a mano")
	}
	if s.Language.Value == "" {
		s.warn("no pude identificar el lenguaje del backend — no se va a poder usar 'asterion plugin build'")
	}
	return s, nil
}

func readFile(dir, rel string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return "", false
	}
	return string(data), true
}
