package vercel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"asterion-core/internal/adapters"
	"asterion-core/internal/capabilities"
)

// withMockProjectsServer apunta projectsURL a un httptest.Server durante el
// test y lo restaura al terminar — projectsURL es una var justo para esto,
// ver el comentario en adapter.go.
func withMockProjectsServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	original := projectsURL
	projectsURL = srv.URL
	t.Cleanup(func() { projectsURL = original })
	return srv
}

func TestListInstances_Success(t *testing.T) {
	var gotAuth, gotQuery string
	withMockProjectsServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"projects": []map[string]string{
				{"id": "prj_abc123", "name": "mi-sitio"},
				{"id": "prj_def456", "name": "mi-api"},
			},
		})
	})

	a := New()
	results, err := a.ListInstances(context.Background(), adapters.DiscoveryQuery{
		Credentials: map[string]string{"token": "tok_test", "team_id": "team_xyz"},
	})
	if err != nil {
		t.Fatalf("ListInstances devolvió error inesperado: %v", err)
	}
	if gotAuth != "Bearer tok_test" {
		t.Errorf("Authorization header = %q, quería %q", gotAuth, "Bearer tok_test")
	}
	if gotQuery != "teamId=team_xyz" {
		t.Errorf("query string = %q, quería %q", gotQuery, "teamId=team_xyz")
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, quería 2", len(results))
	}
	if results[0].ExternalID != "prj_abc123" || results[0].Status != "active" {
		t.Errorf("results[0] = %+v, no coincide con lo esperado", results[0])
	}
	if results[1].ExternalID != "prj_def456" {
		t.Errorf("results[1] = %+v, no coincide con lo esperado", results[1])
	}
}

func TestListInstances_NoTeamID_NoQueryString(t *testing.T) {
	var gotQuery string
	withMockProjectsServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{}})
	})

	a := New()
	if _, err := a.ListInstances(context.Background(), adapters.DiscoveryQuery{
		Credentials: map[string]string{"token": "tok_test"},
	}); err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("query string = %q, quería vacío (sin team_id)", gotQuery)
	}
}

func TestListInstances_EmptyToken(t *testing.T) {
	a := New()
	_, err := a.ListInstances(context.Background(), adapters.DiscoveryQuery{Credentials: map[string]string{}})
	if err == nil {
		t.Fatal("quería un error por token faltante, no hubo ninguno")
	}
}

func TestListInstances_NonOKStatus(t *testing.T) {
	withMockProjectsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid token"}}`))
	})

	a := New()
	_, err := a.ListInstances(context.Background(), adapters.DiscoveryQuery{
		Credentials: map[string]string{"token": "tok_malo"},
	})
	if err == nil {
		t.Fatal("quería un error por status 403, no hubo ninguno")
	}
}

func TestListInstances_InvalidJSON(t *testing.T) {
	withMockProjectsServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("esto no es json"))
	})

	a := New()
	_, err := a.ListInstances(context.Background(), adapters.DiscoveryQuery{
		Credentials: map[string]string{"token": "tok_test"},
	})
	if err == nil {
		t.Fatal("quería un error por JSON inválido, no hubo ninguno")
	}
}

func TestCapabilities(t *testing.T) {
	a := New()
	caps := a.Capabilities()
	if !caps.Has(capabilities.Discovery) {
		t.Error("Vercel debería declarar capabilities.Discovery (es lo que hace callable a ListInstances)")
	}
	if caps.Has(capabilities.Network) || caps.Has(capabilities.Database) || caps.Has(capabilities.Storage) {
		t.Error("Vercel no debería declarar Network/Database/Storage — no tiene esos recursos en el sentido que modela este contrato")
	}
}
