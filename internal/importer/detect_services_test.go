package importer

import "testing"

func TestImageToKindReconoceLosCuatroMotoresYLaVersion(t *testing.T) {
	cases := []struct {
		image       string
		wantKind    string
		wantVersion string
	}{
		{"postgres:16-alpine", "postgres", "16"},
		{"postgres:latest", "postgres", ""},
		{"postgis/postgis:15-3.3", "postgres", "15"}, // el primer '-' corta el tag
		{"mysql:8.0", "mysql", "8.0"},
		{"mariadb:11", "mariadb", "11"},
		{"redis:7", "redis", "7"},
		{"redis", "redis", ""},
		{"nginx:alpine", "", ""},
	}
	for _, c := range cases {
		kind, version := imageToKind(c.image)
		if kind != c.wantKind || (kind != "" && version != c.wantVersion) {
			t.Errorf("imageToKind(%q) = (%q, %q), esperaba (%q, %q)", c.image, kind, version, c.wantKind, c.wantVersion)
		}
	}

	// Un ':' de puerto de registry (antes del '/') no tiene que confundirse
	// con el tag de la imagen — acá alcanza con que no rompa ni detecte un
	// Kind falso; cuál quede en 'version' es un detalle sin consecuencias
	// ya que el caller ignora el resultado cuando Kind es "".
	if kind, _ := imageToKind("myregistry.io:5000/app:1.0"); kind != "" {
		t.Errorf("myregistry.io:5000/app:1.0 no es ninguno de los 4 motores, dio Kind=%q", kind)
	}
}

func TestKindFromURLValue(t *testing.T) {
	cases := map[string]string{
		"postgresql://u:p@h:5432/d": "postgres",
		"postgres://u:p@h:5432/d":   "postgres",
		"mysql://u:p@h:3306/d":      "mysql",
		"redis://h:6379":            "redis",
		"rediss://h:6379":           "redis",
		"mongodb://h:27017/d":       "",
		"":                          "",
	}
	for url, want := range cases {
		if got := kindFromURLValue(url); got != want {
			t.Errorf("kindFromURLValue(%q) = %q, esperaba %q", url, got, want)
		}
	}
}

func TestServicesFromEnvPatternsDetectaURLYFamiliaDeCampos(t *testing.T) {
	pairs := []envPair{
		{Key: "DATABASE_URL", Value: "postgresql://u:p@localhost:5432/d"},
		{Key: "REDIS_URL", Value: "redis://localhost:6379"},
	}
	out := servicesFromEnvPatterns(pairs, nil)
	if len(out) != 2 {
		t.Fatalf("esperaba 2 servicios, hubo %d: %+v", len(out), out)
	}
	byKind := map[string]ServiceCandidate{}
	for _, c := range out {
		byKind[c.Kind] = c
	}
	if byKind["postgres"].MapsURL != "DATABASE_URL" {
		t.Errorf("postgres.MapsURL = %q, esperaba DATABASE_URL", byKind["postgres"].MapsURL)
	}
	if byKind["redis"].MapsURL != "REDIS_URL" {
		t.Errorf("redis.MapsURL = %q, esperaba REDIS_URL", byKind["redis"].MapsURL)
	}
}

func TestServicesFromEnvPatternsFamiliaDeCamposSueltos(t *testing.T) {
	pairs := []envPair{
		{Key: "POSTGRES_HOST", Value: "localhost"},
		{Key: "POSTGRES_PORT", Value: "5432"},
		{Key: "POSTGRES_USER", Value: "app"},
		{Key: "POSTGRES_PASSWORD", Value: ""},
		{Key: "POSTGRES_DATABASE", Value: "appdb"},
	}
	out := servicesFromEnvPatterns(pairs, nil)
	if len(out) != 1 {
		t.Fatalf("esperaba 1 servicio, hubo %d", len(out))
	}
	c := out[0]
	if c.Kind != "postgres" || c.MapsHost != "POSTGRES_HOST" || c.MapsPort != "POSTGRES_PORT" ||
		c.MapsUser != "POSTGRES_USER" || c.MapsPassword != "POSTGRES_PASSWORD" || c.MapsDatabase != "POSTGRES_DATABASE" {
		t.Errorf("mapeo incorrecto: %+v", c)
	}
}

// El caso real que valida el desempate por dependencias: un DATABASE_URL
// genérico, SIN esquema en su valor de ejemplo (vacío, como casi siempre
// aparece en un .env.example real), solo se resuelve si hay una
// dependencia que lo desambigua.
func TestServicesFromEnvPatternsDesempataPorDependencia(t *testing.T) {
	pairs := []envPair{{Key: "DATABASE_URL", Value: ""}}

	if out := servicesFromEnvPatterns(pairs, nil); len(out) != 0 {
		t.Fatalf("sin hints no debería resolver ningún Kind, resolvió: %+v", out)
	}
	out := servicesFromEnvPatterns(pairs, map[string]bool{"postgres": true})
	if len(out) != 1 || out[0].Kind != "postgres" {
		t.Fatalf("con hint de psycopg2 debería resolver postgres, dio: %+v", out)
	}
}

// Redis no tiene usuario ni base — un POSTGRES_* con USER/DATABASE no
// debería "ensuciar" el candidato Redis si alguien declaró ambos
// prefijos (caso raro pero posible).
func TestServicesFromEnvPatternsRedisNoMapeaUserNiDatabase(t *testing.T) {
	pairs := []envPair{
		{Key: "REDIS_HOST", Value: "localhost"},
		{Key: "REDIS_PASSWORD", Value: "x"},
	}
	out := servicesFromEnvPatterns(pairs, nil)
	if len(out) != 1 {
		t.Fatalf("esperaba 1, hubo %d", len(out))
	}
	if out[0].MapsUser != "" || out[0].MapsDatabase != "" {
		t.Errorf("redis no debería mapear user/database: %+v", out[0])
	}
	if out[0].MapsPassword != "REDIS_PASSWORD" {
		t.Errorf("redis debería mapear la password: %+v", out[0])
	}
}

func TestMergeIntoRellenaSinPisarLoYaResuelto(t *testing.T) {
	dst := &ServiceCandidate{Kind: "postgres", Version: "16", Source: "docker-compose.yml"}
	mergeInto(dst, ServiceCandidate{Kind: "postgres", Version: "15", MapsURL: "DATABASE_URL", Source: "prisma/schema.prisma", Confidence: Declared})

	if dst.Version != "16" {
		t.Errorf("no debería pisar un Version ya resuelto: quedó %q", dst.Version)
	}
	if dst.MapsURL != "DATABASE_URL" {
		t.Errorf("debería haber rellenado MapsURL, quedó %q", dst.MapsURL)
	}
}

// Confirma la corrección de un bug real: un servicio mapeado por maps_url
// TIENE que terminar declarado en config_schema (como secret), no
// excluido — ver TestGenerateProduceUnArchivoQueCompila en
// generate_test.go para la prueba end-to-end contra el compilador real.
func TestEnsureConfigFuerzaSecretYLimpiaElDefaultDeEjemplo(t *testing.T) {
	s := &Scan{Configs: []ConfigKey{
		{Key: "DATABASE_URL", Type: "string", Required: false, Default: "postgresql://user:pass@localhost/d"},
	}}
	ensureConfig(s, "DATABASE_URL", true, "test")

	if len(s.Configs) != 1 {
		t.Fatalf("no debería duplicar la entrada existente, quedaron %d", len(s.Configs))
	}
	c := s.Configs[0]
	if c.Type != "secret" {
		t.Errorf("Type = %q, esperaba \"secret\"", c.Type)
	}
	if c.Default != "" {
		t.Errorf("Default debería quedar vacío (era un ejemplo, no un valor real), quedó %q", c.Default)
	}
	if !c.Required {
		t.Error("una clave que un servicio va a resolver tiene que ser Required")
	}
}

func TestEnsureConfigSintetizaCuandoNoExistia(t *testing.T) {
	s := &Scan{}
	ensureConfig(s, "DATABASE_URL", true, "prisma/schema.prisma")
	if len(s.Configs) != 1 || s.Configs[0].Type != "secret" {
		t.Fatalf("esperaba sintetizar DATABASE_URL como secret, quedó %+v", s.Configs)
	}
}
