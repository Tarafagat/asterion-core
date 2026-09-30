package pluginsvc

import (
	"testing"

	"github.com/Tarafagat/asterion-plugin-contract/apc"
)

// Los casos de acá salieron de bugs reales encontrados probando el comando
// en vivo, no de imaginar qué podría fallar. Cada uno fija una decisión que
// ya se rompió una vez.

// Una contraseña con los caracteres que parten una URL tiene que sobrevivir
// el viaje de ida y vuelta. Con un Sprintf, "pa@ss/word" convertía
// postgresql://u:pa@ss/word@host/db en una URL que apunta a otro host.
func TestBuildURLYStoredPasswordSobrevivenCaracteresQueRompenURLs(t *testing.T) {
	spec := apc.ServiceSpec{
		Name: "db", Kind: KindPostgres,
		MapsURL: "DATABASE_URL",
	}
	for _, password := range []string{
		"simple",
		"pa@ss/word",
		"con:dos:puntos",
		"con?query&cosas",
		"con#numeral",
		"barra\\invertida",
		"vUNokx7tp5FA_8HoNRvrqJJAXlDxMtfT", // el formato que genera Asterion
	} {
		res := &Resolution{
			Host: "db.interno", Port: 5432,
			User: "app", Password: password, Database: "midb",
			Values: map[string]string{},
		}
		res.URL = buildURL(KindPostgres, res)
		fillValues(spec, res)

		got := StoredPassword(spec, res.Values)
		if got != password {
			t.Errorf("la contraseña no volvió igual desde la URL\n  guardada: %q\n  URL:      %q\n  leída:    %q",
				password, res.URL, got)
		}
	}
}

// StoredPassword tiene que encontrar la contraseña en cualquiera de las dos
// formas válidas de declararla. Si no la encuentra en la URL, un servicio
// declarado solo con maps_url pide --rotate-password en cada corrida.
func TestStoredPasswordMiraMapsPasswordYTambienLaURL(t *testing.T) {
	cases := []struct {
		nombre string
		spec   apc.ServiceSpec
		config map[string]string
		quiere string
	}{
		{
			nombre: "clave propia",
			spec:   apc.ServiceSpec{MapsPassword: "DB_PASSWORD"},
			config: map[string]string{"DB_PASSWORD": "secreta"},
			quiere: "secreta",
		},
		{
			nombre: "solo URL",
			spec:   apc.ServiceSpec{MapsURL: "DATABASE_URL"},
			config: map[string]string{"DATABASE_URL": "postgresql://app:desdelaurl@h:5432/d"},
			quiere: "desdelaurl",
		},
		{
			nombre: "las dos: gana la clave propia",
			spec:   apc.ServiceSpec{MapsPassword: "DB_PASSWORD", MapsURL: "DATABASE_URL"},
			config: map[string]string{"DB_PASSWORD": "propia", "DATABASE_URL": "postgresql://app:otra@h:5432/d"},
			quiere: "propia",
		},
		{
			nombre: "URL sin contraseña",
			spec:   apc.ServiceSpec{MapsURL: "DATABASE_URL"},
			config: map[string]string{"DATABASE_URL": "postgresql://h:5432/d"},
			quiere: "",
		},
		{
			nombre: "nada guardado",
			spec:   apc.ServiceSpec{MapsPassword: "DB_PASSWORD", MapsURL: "DATABASE_URL"},
			config: map[string]string{},
			quiere: "",
		},
		{
			nombre: "URL que no parsea no explota",
			spec:   apc.ServiceSpec{MapsURL: "DATABASE_URL"},
			config: map[string]string{"DATABASE_URL": "esto no es una url ://"},
			quiere: "",
		},
	}
	for _, c := range cases {
		t.Run(c.nombre, func(t *testing.T) {
			if got := StoredPassword(c.spec, c.config); got != c.quiere {
				t.Errorf("StoredPassword = %q, esperaba %q", got, c.quiere)
			}
		})
	}
}

// El punto más delicado de todo el paquete: qué contraseña queda cuando el
// usuario ya existe en el motor. Antes se generaba una nueva, no se aplicaba
// (CREATE USER IF NOT EXISTS no hace nada si existe) y se guardaba igual —
// una credencial que no funcionaba, reportada como éxito.
func TestDecidePasswordNoDejaGuardarUnaCredencialQueNoVaAFuncionar(t *testing.T) {
	t.Run("no existe: se crea con la generada", func(t *testing.T) {
		res := &Resolution{User: "app", Password: "generada"}
		action, err := decidePassword(false, ProvisionOptions{}, res)
		if err != nil {
			t.Fatalf("no esperaba error: %v", err)
		}
		if res.Password != "generada" {
			t.Errorf("contraseña = %q, esperaba la generada", res.Password)
		}
		if action == "" {
			t.Error("esperaba que dijera qué hizo")
		}
	})

	t.Run("existe sin contraseña guardada: corta", func(t *testing.T) {
		res := &Resolution{User: "app", Password: "generada"}
		_, err := decidePassword(true, ProvisionOptions{}, res)
		if err == nil {
			t.Fatal("tiene que fallar: guardaría una contraseña que el motor no tiene")
		}
	})

	t.Run("existe con contraseña guardada: la reusa, no rota", func(t *testing.T) {
		res := &Resolution{User: "app", Password: "generada"}
		if _, err := decidePassword(true, ProvisionOptions{ExistingPassword: "laguardada"}, res); err != nil {
			t.Fatalf("no esperaba error: %v", err)
		}
	})

	t.Run("existe y se pidió rotar: rota", func(t *testing.T) {
		res := &Resolution{User: "app", Password: "generada"}
		action, err := decidePassword(true, ProvisionOptions{RotatePassword: true}, res)
		if err != nil {
			t.Fatalf("no esperaba error: %v", err)
		}
		if res.Password != "generada" {
			t.Errorf("contraseña = %q, esperaba la nueva", res.Password)
		}
		if action == "" {
			t.Error("rotar una contraseña tiene que quedar dicho")
		}
	})
}

// fillValues nunca puede inventar una clave que el manifiesto no declaró:
// escribir en la config del plugin algo que su config_schema no tiene sería
// basura que nadie lee.
func TestFillValuesSoloEscribeLasClavesDeclaradas(t *testing.T) {
	spec := apc.ServiceSpec{
		Name: "db", Kind: KindPostgres,
		MapsHost: "DB_HOST", MapsPassword: "DB_PASSWORD",
		// a propósito sin maps_port, maps_user, maps_database ni maps_url
	}
	res := &Resolution{
		Host: "h", Port: 5432, User: "u", Password: "p", Database: "d",
		URL: "postgresql://u:p@h:5432/d", Values: map[string]string{},
	}
	fillValues(spec, res)

	if len(res.Values) != 2 {
		t.Fatalf("escribió %d claves (%v), esperaba solo las 2 declaradas", len(res.Values), res.Values)
	}
	if res.Values["DB_HOST"] != "h" || res.Values["DB_PASSWORD"] != "p" {
		t.Errorf("valores mal mapeados: %v", res.Values)
	}
}

// Un puerto guardado que no es un puerto no se puede ignorar y seguir con el
// estándar: el plugin le hablaría a otro servidor mientras el reporte dice
// que está todo bien.
func TestDetectNoSeTragaUnPuertoInvalido(t *testing.T) {
	spec := apc.ServiceSpec{Name: "db", Kind: KindPostgres, MapsHost: "DB_HOST", MapsPort: "DB_PORT"}

	for _, malo := range []string{"127.0.0.1", "noesunpuerto", "0", "99999", "-1", "5432abc"} {
		st := Detect(spec, "plug", map[string]string{"DB_HOST": "127.0.0.1", "DB_PORT": malo})
		if st.BadPort != malo {
			t.Errorf("DB_PORT=%q: BadPort = %q, esperaba %q", malo, st.BadPort, malo)
		}
		if st.Ready() {
			t.Errorf("DB_PORT=%q: Ready() dio true — un puerto inválido nunca está listo", malo)
		}
		if st.Port != defaultPort(KindPostgres) {
			t.Errorf("DB_PORT=%q: Port = %d, esperaba no haberlo tomado", malo, st.Port)
		}
	}

	st := Detect(spec, "plug", map[string]string{"DB_HOST": "127.0.0.1", "DB_PORT": " 6543 "})
	if st.BadPort != "" || st.Port != 6543 {
		t.Errorf("un puerto válido con espacios: BadPort=%q Port=%d, esperaba 6543", st.BadPort, st.Port)
	}
}

// Configured mira solo los maps_* que el manifiesto declaró. Un servicio que
// no mapea usuario no puede quedar "sin configurar" para siempre por una
// clave que nadie pidió.
func TestDetectSoloExigeLoQueElManifiestoDeclaro(t *testing.T) {
	soloURL := apc.ServiceSpec{Name: "db", Kind: KindPostgres, MapsURL: "DATABASE_URL"}

	st := Detect(soloURL, "plug", map[string]string{"DATABASE_URL": "postgresql://u:p@h:5432/d"})
	if !st.Configured {
		t.Error("con su única clave declarada cargada, tiene que contar como configurado")
	}

	st = Detect(soloURL, "plug", map[string]string{})
	if st.Configured {
		t.Error("sin la clave declarada, no puede contar como configurado")
	}

	// Un valor en blanco no es un valor.
	st = Detect(soloURL, "plug", map[string]string{"DATABASE_URL": "   "})
	if st.Configured {
		t.Error("una clave con solo espacios no cuenta como configurada")
	}
}

// Las contraseñas generadas tienen que ser distintas entre sí y seguras de
// poner en una URL sin escapar.
func TestGeneratePasswordDaClavesDistintasYSinCaracteresProblematicos(t *testing.T) {
	vistas := map[string]bool{}
	for i := 0; i < 50; i++ {
		pw, err := generatePassword()
		if err != nil {
			t.Fatalf("no pudo generar: %v", err)
		}
		if len(pw) < 24 {
			t.Errorf("contraseña muy corta (%d): %q", len(pw), pw)
		}
		if vistas[pw] {
			t.Fatalf("repitió una contraseña: %q", pw)
		}
		vistas[pw] = true

		for _, c := range pw {
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
			if !ok {
				t.Errorf("carácter %q en %q — no es seguro sin escapar en una URL", c, pw)
			}
		}
	}
}

// El nombre del contenedor lleva el plugin adentro para que dos plugins que
// necesiten un postgres cada uno no se pisen, y tiene que ser un nombre que
// Docker acepte.
func TestContainerNameAislaCadaPluginYSaneaElNombre(t *testing.T) {
	a := ContainerName("fuelity_bot", "db")
	b := ContainerName("otro-plugin", "db")
	if a == b {
		t.Error("dos plugins distintos no pueden compartir el nombre del contenedor")
	}
	if got := ContainerName("Mi Plugin!", "Cache Principal"); got != "asterion-mi-plugin--cache-principal" {
		t.Errorf("ContainerName no saneó bien: %q", got)
	}
}

// Escapar es lo correcto aunque estos valores vengan del plugin.yaml: ese
// manifiesto lo escribe un tercero.
func TestSqlIdentYSqlLitEscapan(t *testing.T) {
	if got := sqlIdent(`mi"base`); got != `mi""base` {
		t.Errorf("sqlIdent = %q", got)
	}
	if got := sqlLit(`o'brien`); got != `o''brien` {
		t.Errorf("sqlLit = %q", got)
	}
	if got := sqlLit(`con\barra`); got != `con\\barra` {
		t.Errorf("sqlLit = %q", got)
	}
}
