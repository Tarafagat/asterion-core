package importer

import "regexp"

// detectPort busca en .env.example primero (si el proyecto se tomó el
// trabajo de documentar su propio PORT, es la fuente más confiable), y
// si no, docker-compose.yml. detectLanguageAndStart ya pudo haberlo
// llenado desde un Dockerfile EXPOSE — eso no se pisa, es igual de
// confiable y corrió primero.
//
// Sin ninguna señal, el valor es "0" — y eso NO es una falla: es
// literalmente lo que ya hacen los plugins reales de este ecosistema
// (Contract.start(..., port=0)) para decir "Asterion elige un puerto
// libre". Es el default correcto, no un placeholder.
func detectPort(s *Scan) {
	if s.Port.Value != "" {
		return
	}
	if text, ok := readFile(s.Dir, ".env.example"); ok {
		if v := envValue(text, "PORT"); v != "" {
			s.Port = Finding{Value: v, Confidence: Declared, Source: ".env.example"}
			return
		}
	}
	if text, ok := readFile(s.Dir, "docker-compose.yml"); ok {
		if m := composePortRE.FindStringSubmatch(text); m != nil {
			s.Port = Finding{Value: m[1], Confidence: Declared, Source: "docker-compose.yml"}
			return
		}
	} else if text, ok := readFile(s.Dir, "docker-compose.yaml"); ok {
		if m := composePortRE.FindStringSubmatch(text); m != nil {
			s.Port = Finding{Value: m[1], Confidence: Declared, Source: "docker-compose.yaml"}
			return
		}
	}
	s.Port = Finding{Value: "0", Confidence: Declared, Source: "default — Asterion elige un puerto libre"}
}

// composePortRE busca el primer mapeo "HOST:CONTAINER" bajo una línea
// 'ports:' — no diferencia a qué servicio pertenece (un parseo completo
// de YAML por servicio ya lo hace detect_services.go para las bases; acá
// alcanza con la primera coincidencia porque es solo una pista de puerto,
// no algo que vaya a crear nada).
var composePortRE = regexp.MustCompile(`(?m)^\s*-\s*"?(\d{2,5}):\d{2,5}"?`)
