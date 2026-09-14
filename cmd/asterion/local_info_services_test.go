package main

import (
	"testing"

	"asterion-core/internal/sysservices"
)

func TestWantedCategories_EmptyWhenNoFlags(t *testing.T) {
	if got := wantedCategories(false, false, false, false); len(got) != 0 {
		t.Errorf("wantedCategories(sin flags) = %v, want vacío (sin filtro)", got)
	}
}

func TestWantedCategories_CombinesFlags(t *testing.T) {
	got := wantedCategories(true, true, false, false)
	if !got[sysservices.CategorySystem] || !got[sysservices.CategoryDatabase] {
		t.Errorf("wantedCategories(system,database) = %v, quería ambas categorías presentes", got)
	}
	if got[sysservices.CategoryAPI] || got[sysservices.CategoryOther] {
		t.Errorf("wantedCategories(system,database) = %v, no debería incluir api/other", got)
	}
}

func TestFilterByCategory_EmptyWantedReturnsAll(t *testing.T) {
	units := []sysservices.Unit{{Name: "nginx.service", Category: sysservices.CategoryAPI}, {Name: "ssh.service", Category: sysservices.CategorySystem}}
	got := filterByCategory(units, map[sysservices.Category]bool{})
	if len(got) != 2 {
		t.Fatalf("filterByCategory(sin filtro) devolvió %d, esperaba las 2 unidades sin filtrar", len(got))
	}
}

func TestFilterByCategory_KeepsOnlyWanted(t *testing.T) {
	units := []sysservices.Unit{
		{Name: "nginx.service", Category: sysservices.CategoryAPI},
		{Name: "ssh.service", Category: sysservices.CategorySystem},
		{Name: "mysql.service", Category: sysservices.CategoryDatabase},
	}
	got := filterByCategory(units, map[sysservices.Category]bool{sysservices.CategoryAPI: true, sysservices.CategoryDatabase: true})
	if len(got) != 2 {
		t.Fatalf("filterByCategory(api,database) devolvió %d unidades, esperaba 2", len(got))
	}
	for _, u := range got {
		if u.Category == sysservices.CategorySystem {
			t.Errorf("filterByCategory(api,database) no debería incluir %q (system)", u.Name)
		}
	}
}
