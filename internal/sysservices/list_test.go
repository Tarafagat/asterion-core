package sysservices

import (
	"reflect"
	"testing"
)

func TestParsePlain(t *testing.T) {
	// Muestra real de `systemctl list-units --all --type=service --no-legend
	// --no-pager --plain --full` — ancho de columnas variable (alineación
	// real de systemd), incluida una unidad "not-found" (LOAD distinto de
	// "loaded") y una descripción de varias palabras, para ejercitar el
	// join de la columna 5 en vez de un split ingenuo.
	sample := "" +
		"nginx.service                     loaded active   running High performance web server\n" +
		"mysql.service                     loaded failed   failed  MySQL Community Server\n" +
		"asterion-agent-inst_1.service      loaded active   running Asterion agent (inst_1)\n" +
		"nonexistent.service                not-found inactive dead  \n"

	got := parsePlain([]byte(sample))
	want := []Unit{
		{Name: "nginx.service", LoadState: "loaded", ActiveState: "active", SubState: "running", Description: "High performance web server"},
		{Name: "mysql.service", LoadState: "loaded", ActiveState: "failed", SubState: "failed", Description: "MySQL Community Server"},
		{Name: "asterion-agent-inst_1.service", LoadState: "loaded", ActiveState: "active", SubState: "running", Description: "Asterion agent (inst_1)"},
		{Name: "nonexistent.service", LoadState: "not-found", ActiveState: "inactive", SubState: "dead"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePlain() = %#v, want %#v", got, want)
	}
}

func TestParsePlain_BlankAndShortLinesIgnored(t *testing.T) {
	sample := "\n   \nnginx.service loaded active running\n"
	got := parsePlain([]byte(sample))
	if len(got) != 1 || got[0].Name != "nginx.service" {
		t.Fatalf("parsePlain() = %#v, esperaba una sola unidad nginx.service", got)
	}
}

func TestParseJSON(t *testing.T) {
	sample := `[
		{"unit":"nginx.service","load":"loaded","active":"active","sub":"running","description":"High performance web server"},
		{"unit":"mysql.service","load":"loaded","active":"failed","sub":"failed","description":"MySQL Community Server"}
	]`
	got, err := parseJSON([]byte(sample))
	if err != nil {
		t.Fatalf("parseJSON() error = %v", err)
	}
	want := []Unit{
		{Name: "nginx.service", LoadState: "loaded", ActiveState: "active", SubState: "running", Description: "High performance web server"},
		{Name: "mysql.service", LoadState: "loaded", ActiveState: "failed", SubState: "failed", Description: "MySQL Community Server"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseJSON() = %#v, want %#v", got, want)
	}
}

func TestParseJSON_InvalidFallsBackToError(t *testing.T) {
	if _, err := parseJSON([]byte("no es json")); err == nil {
		t.Fatal("parseJSON() con basura debería devolver error para que List() caiga al parser de texto plano")
	}
}

func TestAnnotate_MarksProtected(t *testing.T) {
	units := annotate([]Unit{{Name: "ssh.service"}, {Name: "nginx.service"}, {Name: "asterion-agent-inst_1.service"}})
	want := map[string]bool{"ssh.service": true, "nginx.service": false, "asterion-agent-inst_1.service": true}
	for _, u := range units {
		if u.Protected != want[u.Name] {
			t.Errorf("annotate(%q).Protected = %v, want %v", u.Name, u.Protected, want[u.Name])
		}
	}
}
