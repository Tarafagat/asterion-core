package sysservices

import "testing"

func TestClassify(t *testing.T) {
	cases := map[string]Category{
		// system
		"ssh.service":                CategorySystem,
		"sshd.service":               CategorySystem,
		"NetworkManager.service":     CategorySystem,
		"cron.service":               CategorySystem,
		"ufw.service":                CategorySystem,
		"getty@tty1.service":         CategorySystem, // unidad templada
		"serial-getty@ttyS0.service": CategorySystem,

		// database
		"mysql.service":              CategoryDatabase,
		"mariadb.service":            CategoryDatabase,
		"postgresql.service":         CategoryDatabase,
		"postgresql@14-main.service": CategoryDatabase, // unidad templada
		"redis-server.service":       CategoryDatabase,
		"mongod.service":             CategoryDatabase,

		// api: motores conocidos
		"nginx.service":      CategoryAPI,
		"caddy.service":      CategoryAPI,
		"docker.service":     CategoryAPI,
		"containerd.service": CategoryAPI,

		// api: heurística de sufijo para servicios de aplicación propios
		"asterion-cloud-backend.service": CategoryAPI,
		"asterion-core-backend.service":  CategoryAPI,
		"mi-app-api.service":             CategoryAPI,
		"pedidos-app.service":            CategoryAPI,

		// other: no matchea nada conocido — nunca se fuerza a system/api
		"asterion-agent-inst_1.service": CategoryOther,
		"random-cron-job.service":       CategoryOther,
		"openvpn.service":               CategoryOther,
	}
	for name, want := range cases {
		if got := Classify(name); got != want {
			t.Errorf("Classify(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestClassify_CaseInsensitive(t *testing.T) {
	if got := Classify("MySQL.service"); got != CategoryDatabase {
		t.Errorf("Classify(%q) = %q, want %q (debería ser insensible a mayúsculas)", "MySQL.service", got, CategoryDatabase)
	}
}

func TestClassify_SubstringFallbackForNonSystemdNames(t *testing.T) {
	// Nombres con forma de label de launchd o de servicio de Windows (sin
	// ".service") — el match exacto nunca les va a pegar, así que tienen
	// que resolver por el paso de substring.
	cases := map[string]Category{
		"com.vix.cron":        CategorySystem,   // launchd (macOS)
		"org.cups.cupsd":      CategorySystem,   // launchd (macOS)
		"com.openssh.sshd":    CategorySystem,   // launchd (macOS)
		"postgresql-x64-14":   CategoryDatabase, // Windows (instalador oficial)
		"MySQL80":             CategoryDatabase, // Windows (instalador oficial)
		"com.docker.docker":   CategoryAPI,      // launchd (macOS, Docker Desktop)
		"aleatorio-sin-match": CategoryOther,
	}
	for name, want := range cases {
		if got := Classify(name); got != want {
			t.Errorf("Classify(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestClassify_SubstringFallbackNeverAppliesToServiceSuffixedNames(t *testing.T) {
	// Un nombre que SÍ termina en ".service" (forma systemd real) nunca
	// debe pasar por el fallback de substring, aunque contenga una
	// substring conocida — el match exacto es la única palabra final ahí,
	// mismo comportamiento que antes de agregar el fallback.
	if got := Classify("random-cron-job.service"); got != CategoryOther {
		t.Errorf("Classify(%q) = %q, want %q (no debería pasar por el fallback de substring)", "random-cron-job.service", got, CategoryOther)
	}
}

func TestClassify_MoreSpecificListsWinOverSuffixHeuristic(t *testing.T) {
	// "docker" ya está en apiUnits (lista curada); confirma que no hace
	// falta la heurística de sufijo para que algo así clasifique bien, y
	// que ninguna de las dos reglas se pisa entre sí.
	if got := Classify("docker.service"); got != CategoryAPI {
		t.Errorf("Classify(docker.service) = %q, want %q", got, CategoryAPI)
	}
}
