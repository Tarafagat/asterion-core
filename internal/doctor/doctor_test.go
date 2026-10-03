package doctor

import "testing"

func TestWorstEsLaSeveridadMasAlta(t *testing.T) {
	r := Report{Checks: []Check{
		{Severity: OK}, {Severity: Warn}, {Severity: NA}, {Severity: OK},
	}}
	if got := r.Worst(); got != Warn {
		t.Errorf("Worst() = %v, esperaba Warn", got)
	}
	if r2 := (Report{}); r2.Worst() != NA {
		t.Errorf("un reporte sin checks tiene que dar NA, dio %v", r2.Worst())
	}
}

func TestBySectionPreservaElPrimerOrden(t *testing.T) {
	r := Report{Checks: []Check{
		{Section: "Health"}, {Section: "Security"}, {Section: "Health"}, {Section: "AI"},
	}}
	got := r.BySection()
	want := []string{"Health", "Security", "AI"}
	if len(got) != len(want) {
		t.Fatalf("BySection() = %v, esperaba %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("BySection()[%d] = %q, esperaba %q", i, got[i], want[i])
		}
	}
}

// Este es el bug real que se encontró probando en vivo: con un Warn de
// ambiente apareciendo ANTES que un Fail de seguridad en la lista de
// checks, el resumen de una línea mostraba el Warn — el marcador decía
// "grave" pero el texto no explicaba por qué. summarize tiene que mostrar
// el check de la severidad más alta, sin importar en qué orden corrieron.
func TestSummarizeMuestraElCheckDeLaPeorSeveridadNoElPrimero(t *testing.T) {
	r := Report{Checks: []Check{
		{Section: "Environment", Name: "Go", Severity: Warn, Detail: "versión distinta"},
		{Section: "Security", Name: "Secretos", Severity: Fail, Detail: "commiteado en el repo"},
	}}
	s := summarize("miplugin", r)
	if s.Worst != Fail {
		t.Fatalf("Worst = %v, esperaba Fail", s.Worst)
	}
	if s.Issue != "Secretos: commiteado en el repo" {
		t.Errorf("Issue = %q, esperaba que mostrara el Fail (Secretos), no el Warn (Go)", s.Issue)
	}
}

func TestSummarizeSinProblemasNoTieneIssue(t *testing.T) {
	r := Report{Checks: []Check{{Severity: OK}, {Severity: NA}}}
	s := summarize("x", r)
	if s.Issue != "" {
		t.Errorf("Issue = %q, esperaba vacío cuando no hay Warn/Fail", s.Issue)
	}
}

func TestDeclaresUnrestricted(t *testing.T) {
	cases := []struct {
		paths []string
		want  bool
	}{
		{[]string{"/"}, true},
		{[]string{"*"}, true},
		{[]string{"0.0.0.0/0"}, true},
		{[]string{"./data"}, false},
		{[]string{"api.stripe.com", "api.github.com"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := declaresUnrestricted(c.paths); got != c.want {
			t.Errorf("declaresUnrestricted(%v) = %v, esperaba %v", c.paths, got, c.want)
		}
	}
}

func TestSeverityMarkerYString(t *testing.T) {
	cases := map[Severity]string{OK: "ok", Warn: "warn", Fail: "fail", NA: "na"}
	for sev, want := range cases {
		if sev.String() != want {
			t.Errorf("%v.String() = %q, esperaba %q", int(sev), sev.String(), want)
		}
		if sev.Marker() == "" {
			t.Errorf("%v.Marker() no puede ser vacío", want)
		}
	}
}
