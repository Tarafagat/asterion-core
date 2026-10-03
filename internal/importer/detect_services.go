package importer

import (
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// detectServices junta tres fuentes independientes y las funde por Kind
// (postgres/mysql/mariadb/redis) — un proyecto real casi nunca declara
// dos bases del mismo motor como "la externa que necesita", así que fundir
// por Kind es seguro y evita triplicar el mismo servicio detectado por
// tres lados distintos:
//
//  1. docker-compose.yml: la imagen de un servicio (postgres:16, redis:7)
//     es la señal más directa que existe — ALGUIEN ya decidió qué motor y
//     qué versión usar, no hay nada que adivinar ahí.
//  2. prisma/schema.prisma: el datasource declara provider+la variable de
//     entorno con la URL completa — tan directo como docker-compose.
//  3. Patrones de variables en el .env de ejemplo (DATABASE_URL, o
//     DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME sueltos) — la señal que
//     cubre el caso más común de todos: un proyecto sin docker-compose que
//     simplemente espera conectarse a una base ya existente en otro lado.
//
// Las dependencias (psycopg2, pymysql, ioredis...) en requirements.txt/
// package.json se usan solo como desempate de Kind cuando una URL
// detectada no trae esquema (DATABASE_URL= sin valor de ejemplo) —  nunca
// alcanzan solas para declarar un servicio: tener psycopg2 instalado no
// prueba que el proyecto lo esté usando hoy.
func detectServices(s *Scan) {
	byKind := map[string]*ServiceCandidate{}
	merge := func(c ServiceCandidate) {
		existing, ok := byKind[c.Kind]
		if !ok {
			cc := c
			byKind[c.Kind] = &cc
			return
		}
		mergeInto(existing, c)
	}

	for _, c := range servicesFromCompose(s) {
		merge(c)
	}
	for _, c := range servicesFromPrisma(s) {
		merge(c)
	}
	_, pairs := findEnvFile(s)
	hints := dependencyHints(s)
	for _, c := range servicesFromEnvPatterns(pairs, hints) {
		merge(c)
	}

	// Orden fijo de Kind (no el de iterar el map, que cambia entre
	// corridas): dos escaneos del mismo proyecto tienen que generar el
	// mismo app.asterion.
	for _, kind := range []string{"postgres", "mysql", "mariadb", "redis"} {
		c, ok := byKind[kind]
		if !ok {
			continue
		}
		s.Services = append(s.Services, *c)
	}

	// Todo maps_* de un servicio TIENE que existir en config_schema — no es
	// opcional, es una regla del contrato (apc.Manifest.Validate() lo
	// rechaza si no): la clave ahí declara DE QUÉ TIPO es, y el service
	// dice qué la llena. No son dos lugares redundantes diciendo lo mismo,
	// es una referencia — sacarla de encima (lo que hacía antes esta
	// función) generaba un app.asterion que no compilaba.
	for _, svc := range s.Services {
		for _, m := range []struct {
			key    string
			secret bool // maps_password/maps_url siempre cargan una credencial,
		}{ // aunque el NOMBRE de la clave no tenga pinta de serlo
			// (DATABASE_URL no contiene "SECRET" ni "PASSWORD" y aun así
			// lleva una contraseña adentro) — se fuerza el tipo acá.
			{svc.MapsHost, false}, {svc.MapsPort, false}, {svc.MapsUser, false},
			{svc.MapsDatabase, false}, {svc.MapsPassword, true}, {svc.MapsURL, true},
		} {
			if m.key != "" {
				ensureConfig(s, m.key, m.secret, svc.Source)
			}
		}
	}

	for _, c := range s.Services {
		if c.MapsHost == "" && c.MapsURL == "" {
			s.warn("detecté un servicio %s (%s) pero no encontré qué variable de entorno usa para conectarse — completá maps_host/maps_port o maps_url a mano en Contract.service(name=%q, ...)",
				c.Name, c.Kind, c.Name)
		}
	}
}

// ensureConfig deja una ConfigKey para 'key' en s.Configs: si ya existía
// (porque .env.example la tenía), solo le fuerza el tipo "secret" cuando
// corresponde, sin perder el Default/Required que ya se había inferido. Si
// no existía (un prisma/schema.prisma sin .env.example al lado, por
// ejemplo), la sintetiza como obligatoria — un servicio sin esa clave
// resuelta no puede funcionar.
func ensureConfig(s *Scan, key string, secret bool, source string) {
	for i, c := range s.Configs {
		if c.Key == key {
			if secret {
				s.Configs[i].Type = "secret"
				// El valor que traía .env.example acá es un EJEMPLO
				// ilustrativo (user:pass@localhost…), no una credencial
				// real que tenga sentido dejar como default — y de todos
				// modos a esta clave la va a llenar 'plugin services
				// up'/'connect', no alguien tipeándola a mano. Dejarlo
				// como default invitaría a pensar que ya está resuelta.
				s.Configs[i].Default = ""
				s.Configs[i].Required = true
			}
			return
		}
	}
	typ := "string"
	if secret {
		typ = "secret"
	}
	s.Configs = append(s.Configs, ConfigKey{Key: key, Type: typ, Required: true, Source: source})
}

func mergeInto(dst *ServiceCandidate, src ServiceCandidate) {
	if dst.Version == "" {
		dst.Version = src.Version
	}
	if dst.Database == "" {
		dst.Database = src.Database
	}
	if dst.User == "" {
		dst.User = src.User
	}
	if dst.MapsHost == "" {
		dst.MapsHost = src.MapsHost
	}
	if dst.MapsPort == "" {
		dst.MapsPort = src.MapsPort
	}
	if dst.MapsUser == "" {
		dst.MapsUser = src.MapsUser
	}
	if dst.MapsPassword == "" {
		dst.MapsPassword = src.MapsPassword
	}
	if dst.MapsDatabase == "" {
		dst.MapsDatabase = src.MapsDatabase
	}
	if dst.MapsURL == "" {
		dst.MapsURL = src.MapsURL
	}
	if src.Confidence > dst.Confidence {
		dst.Confidence = src.Confidence
	}
	if dst.Source != src.Source {
		dst.Source = dst.Source + ", " + src.Source
	}
}

// --- docker-compose.yml -----------------------------------------------

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Image string `yaml:"image"`
}

func servicesFromCompose(s *Scan) []ServiceCandidate {
	var source string
	var text string
	var ok bool
	if text, ok = readFile(s.Dir, "docker-compose.yml"); ok {
		source = "docker-compose.yml"
	} else if text, ok = readFile(s.Dir, "docker-compose.yaml"); ok {
		source = "docker-compose.yaml"
	} else {
		return nil
	}
	s.sawFile(source)

	var doc composeFile
	if yaml.Unmarshal([]byte(text), &doc) != nil {
		s.warn("encontré %s pero no pude interpretarlo como YAML — revisalo a mano", source)
		return nil
	}

	var out []ServiceCandidate
	for name, svc := range doc.Services {
		kind, version := imageToKind(svc.Image)
		if kind == "" {
			continue
		}
		out = append(out, ServiceCandidate{
			Name:       sanitizeName(name),
			Kind:       kind,
			Version:    version,
			Confidence: Declared,
			Source:     source,
		})
	}
	return out
}

func imageToKind(image string) (kind, version string) {
	base := image
	version = ""
	if i := strings.LastIndexByte(image, ':'); i >= 0 && !strings.Contains(image[i:], "/") {
		base, version = image[:i], image[i+1:]
		// "16-alpine" -> "16": solo el número que importa para elegir la
		// imagen al levantar un contenedor, no el sabor.
		if j := strings.IndexByte(version, '-'); j >= 0 {
			version = version[:j]
		}
		if version == "latest" {
			version = ""
		}
	}
	base = strings.ToLower(base)
	switch {
	case strings.Contains(base, "postgis"), strings.Contains(base, "postgres"):
		return "postgres", version
	case strings.Contains(base, "mariadb"):
		return "mariadb", version
	case strings.Contains(base, "mysql"):
		return "mysql", version
	case strings.Contains(base, "redis"):
		return "redis", version
	}
	return "", ""
}

// --- prisma/schema.prisma -----------------------------------------------

var prismaDatasourceRE = regexp.MustCompile(`(?s)datasource\s+\w+\s*\{(.*?)\}`)
var prismaProviderRE = regexp.MustCompile(`provider\s*=\s*"(\w+)"`)
var prismaURLEnvRE = regexp.MustCompile(`url\s*=\s*env\("(\w+)"\)`)

func servicesFromPrisma(s *Scan) []ServiceCandidate {
	text, ok := readFile(s.Dir, "prisma/schema.prisma")
	if !ok {
		return nil
	}
	s.sawFile("prisma/schema.prisma")

	block := prismaDatasourceRE.FindStringSubmatch(text)
	if block == nil {
		return nil
	}
	providerM := prismaProviderRE.FindStringSubmatch(block[1])
	urlM := prismaURLEnvRE.FindStringSubmatch(block[1])
	if providerM == nil || urlM == nil {
		return nil
	}

	var kind string
	switch providerM[1] {
	case "postgresql", "postgres":
		kind = "postgres"
	case "mysql":
		kind = "mysql"
	default:
		s.warn("prisma/schema.prisma declara provider=%q — Asterion sabe detectar/configurar postgres, mysql, mariadb y redis; esa base hay que conectarla a mano", providerM[1])
		return nil
	}

	return []ServiceCandidate{{
		Name: "db", Kind: kind, MapsURL: urlM[1],
		Confidence: Declared, Source: "prisma/schema.prisma",
	}}
}

// --- patrones en .env.example -------------------------------------------

// envPrefixes mapea el prefijo de una familia de variables a su Kind. El
// orden importa: "POSTGRESQL"/"POSTGRES" antes que el genérico "DB", así
// un proyecto que declaró las dos formas no genera un Kind duplicado mal
// resuelto (se procesa cada prefijo una sola vez, ver 'used' abajo).
var envPrefixes = []struct {
	prefix string
	kind   string
}{
	{"POSTGRESQL", "postgres"},
	{"POSTGRES", "postgres"},
	{"MARIADB", "mariadb"},
	{"MYSQL", "mysql"},
	{"REDIS", "redis"},
	{"DB", ""}, // el Kind de "DB_*" genérico sale del esquema de su propia URL, si la hay
}

func servicesFromEnvPatterns(pairs []envPair, hints map[string]bool) []ServiceCandidate {
	byKey := map[string]string{}
	for _, p := range pairs {
		byKey[p.Key] = p.Value
	}

	var out []ServiceCandidate

	// Variables de una sola URL: *_URL, *_URI, *_CONNECTION_STRING.
	for key, value := range byKey {
		upper := strings.ToUpper(key)
		if !strings.HasSuffix(upper, "_URL") && !strings.HasSuffix(upper, "_URI") && !strings.HasSuffix(upper, "_CONNECTION_STRING") {
			continue
		}
		kind := kindFromURLValue(value)
		if kind == "" {
			kind = kindFromName(upper, hints)
		}
		if kind == "" {
			continue
		}
		out = append(out, ServiceCandidate{
			Name: strings.ToLower(kind), Kind: kind, MapsURL: key,
			Confidence: Declared, Source: ".env.example (" + key + ")",
		})
	}

	// Variables sueltas por familia: PREFIX_HOST/PORT/USER/PASSWORD/NAME.
	for _, fam := range envPrefixes {
		host, hasHost := findSuffixed(byKey, fam.prefix, "HOST")
		if !hasHost {
			continue
		}
		kind := fam.kind
		if kind == "" {
			kind = kindFromName(fam.prefix, hints)
		}
		if kind == "" {
			continue
		}
		port, _ := findSuffixed(byKey, fam.prefix, "PORT")
		user, _ := findSuffixed(byKey, fam.prefix, "USER", "USERNAME")
		pass, _ := findSuffixed(byKey, fam.prefix, "PASSWORD", "PASS")
		name, _ := findSuffixed(byKey, fam.prefix, "NAME", "DATABASE", "DB")

		c := ServiceCandidate{Name: strings.ToLower(kind), Kind: kind, MapsHost: host, Confidence: Declared,
			Source: ".env.example (" + fam.prefix + "_*)"}
		if port != "" {
			c.MapsPort = port
		}
		if kind != "redis" {
			c.MapsUser = user
			c.MapsPassword = pass
			c.MapsDatabase = name
		} else {
			c.MapsPassword = pass
		}
		out = append(out, c)
	}
	return out
}

// findSuffixed busca PREFIX_SUFFIX entre las variables, probando cada
// alias de sufijo en orden (ej. "USER" y, si no está, "USERNAME").
func findSuffixed(byKey map[string]string, prefix string, suffixes ...string) (key string, ok bool) {
	for _, suf := range suffixes {
		k := prefix + "_" + suf
		if _, exists := byKey[k]; exists {
			return k, true
		}
	}
	return "", false
}

func kindFromURLValue(value string) string {
	switch {
	case strings.HasPrefix(value, "postgres://"), strings.HasPrefix(value, "postgresql://"):
		return "postgres"
	case strings.HasPrefix(value, "mysql://"):
		return "mysql"
	case strings.HasPrefix(value, "redis://"), strings.HasPrefix(value, "rediss://"):
		return "redis"
	}
	return ""
}

func kindFromName(upperNameOrPrefix string, hints map[string]bool) string {
	switch {
	case strings.Contains(upperNameOrPrefix, "REDIS"):
		return "redis"
	case strings.Contains(upperNameOrPrefix, "MARIADB"):
		return "mariadb"
	case strings.Contains(upperNameOrPrefix, "MYSQL"):
		return "mysql"
	case strings.Contains(upperNameOrPrefix, "POSTGRES"):
		return "postgres"
	}
	// Genérico (DATABASE_URL, DB_HOST): se desempata con qué driver está
	// instalado, si hay uno solo claro.
	switch {
	case hints["postgres"] && !hints["mysql"]:
		return "postgres"
	case hints["mysql"] && !hints["postgres"]:
		return "mysql"
	}
	return ""
}

// dependencyHints mira requirements.txt/pyproject.toml/package.json por
// el NOMBRE de paquetes de driver conocidos — una señal débil, nunca
// suficiente sola (ver el comentario de detectServices), solo sirve para
// desempatar un Kind genérico.
func dependencyHints(s *Scan) map[string]bool {
	hints := map[string]bool{}
	var blob strings.Builder
	for _, f := range []string{"requirements.txt", "pyproject.toml", "package.json", "Pipfile"} {
		if text, ok := readFile(s.Dir, f); ok {
			blob.WriteString(strings.ToLower(text))
			blob.WriteByte('\n')
		}
	}
	text := blob.String()
	for _, dep := range []string{"psycopg2", "psycopg", "asyncpg", "pg\"", "pg "} {
		if strings.Contains(text, dep) {
			hints["postgres"] = true
		}
	}
	for _, dep := range []string{"pymysql", "mysqlclient", "aiomysql", "mysql2", "mysql2\""} {
		if strings.Contains(text, dep) {
			hints["mysql"] = true
		}
	}
	for _, dep := range []string{"redis", "ioredis", "aioredis"} {
		if strings.Contains(text, dep) {
			hints["redis"] = true
		}
	}
	return hints
}
