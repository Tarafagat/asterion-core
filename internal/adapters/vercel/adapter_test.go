package vercel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if !caps.Has(capabilities.Pricing) {
		t.Error("Vercel debería declarar capabilities.Pricing (es lo que hace callable a GetCostReport)")
	}
	if caps.Has(capabilities.Network) || caps.Has(capabilities.Database) || caps.Has(capabilities.Storage) {
		t.Error("Vercel no debería declarar Network/Database/Storage — no tiene esos recursos en el sentido que modela este contrato")
	}
}

// withMockBillingServer apunta billingChargesURL a un httptest.Server
// durante el test y lo restaura al terminar, igual que withMockProjectsServer.
func withMockBillingServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	original := billingChargesURL
	billingChargesURL = srv.URL
	t.Cleanup(func() { billingChargesURL = original })
	return srv
}

func writeJSONL(w http.ResponseWriter, lines ...map[string]any) {
	for _, line := range lines {
		_ = json.NewEncoder(w).Encode(line)
	}
}

func TestGetCostReport_Success_GroupsByService(t *testing.T) {
	var gotAuth, gotQuery string
	withMockBillingServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		writeJSONL(w,
			map[string]any{"BilledCost": 1.5, "ServiceName": "Edge Requests", "ChargeCategory": "Usage", "Tags": map[string]string{"ProjectId": "prj_abc"}},
			map[string]any{"BilledCost": 2.25, "ServiceName": "Edge Requests", "ChargeCategory": "Usage", "Tags": map[string]string{"ProjectId": "prj_abc"}},
			map[string]any{"BilledCost": 0.4, "ServiceName": "Fast Data Transfer", "ChargeCategory": "Usage", "Tags": map[string]string{"ProjectId": "prj_abc"}},
		)
	})

	a := New()
	results, err := a.GetCostReport(context.Background(), adapters.CostReportQuery{
		From:        "2026-08-01T00:00:00.000Z",
		To:          "2026-09-01T00:00:00.000Z",
		ExternalID:  "prj_abc",
		Credentials: map[string]string{"token": "tok_test", "team_id": "team_xyz"},
	})
	if err != nil {
		t.Fatalf("GetCostReport devolvió error inesperado: %v", err)
	}
	if gotAuth != "Bearer tok_test" {
		t.Errorf("Authorization header = %q, quería %q", gotAuth, "Bearer tok_test")
	}
	if !strings.Contains(gotQuery, "teamId=team_xyz") || !strings.Contains(gotQuery, "from=") || !strings.Contains(gotQuery, "to=") {
		t.Errorf("query string = %q, esperaba from/to/teamId", gotQuery)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, quería 2 (agrupado por servicio)", len(results))
	}
	if results[0].ServiceName != "Edge Requests" || results[0].BilledCostUSD != 3.75 {
		t.Errorf("results[0] = %+v, quería Edge Requests con 3.75 (1.5+2.25)", results[0])
	}
	if results[1].ServiceName != "Fast Data Transfer" || results[1].BilledCostUSD != 0.4 {
		t.Errorf("results[1] = %+v, quería Fast Data Transfer con 0.4", results[1])
	}
}

func TestGetCostReport_FiltersByExternalID(t *testing.T) {
	withMockBillingServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSONL(w,
			map[string]any{"BilledCost": 5.0, "ServiceName": "Edge Requests", "Tags": map[string]string{"ProjectId": "prj_abc"}},
			map[string]any{"BilledCost": 9.0, "ServiceName": "Edge Requests", "Tags": map[string]string{"ProjectId": "prj_otro"}},
		)
	})

	a := New()
	results, err := a.GetCostReport(context.Background(), adapters.CostReportQuery{
		From: "2026-08-01T00:00:00.000Z", To: "2026-09-01T00:00:00.000Z",
		ExternalID:  "prj_abc",
		Credentials: map[string]string{"token": "tok_test"},
	})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(results) != 1 || results[0].BilledCostUSD != 5.0 {
		t.Fatalf("results = %+v, quería solo el cargo de prj_abc (5.0)", results)
	}
}

func TestGetCostReport_BilledCostAsQuotedString(t *testing.T) {
	withMockBillingServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSONL(w, map[string]any{"BilledCost": "12.34", "ServiceName": "Function Invocations", "Tags": map[string]string{}})
	})

	a := New()
	results, err := a.GetCostReport(context.Background(), adapters.CostReportQuery{
		From: "2026-08-01T00:00:00.000Z", To: "2026-09-01T00:00:00.000Z",
		Credentials: map[string]string{"token": "tok_test"},
	})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(results) != 1 || results[0].BilledCostUSD != 12.34 {
		t.Fatalf("results = %+v, quería 12.34 aunque BilledCost venga como string", results)
	}
}

func TestGetCostReport_EmptyToken(t *testing.T) {
	a := New()
	_, err := a.GetCostReport(context.Background(), adapters.CostReportQuery{From: "a", To: "b", Credentials: map[string]string{}})
	if err == nil {
		t.Fatal("quería un error por token faltante, no hubo ninguno")
	}
}

func TestGetCostReport_MissingDateRange(t *testing.T) {
	a := New()
	_, err := a.GetCostReport(context.Background(), adapters.CostReportQuery{Credentials: map[string]string{"token": "tok_test"}})
	if err == nil {
		t.Fatal("quería un error por from/to faltantes, no hubo ninguno")
	}
}

func TestGetCostReport_NonOKStatus(t *testing.T) {
	withMockBillingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"invalidToken"}`))
	})

	a := New()
	_, err := a.GetCostReport(context.Background(), adapters.CostReportQuery{
		From: "a", To: "b", Credentials: map[string]string{"token": "tok_malo"},
	})
	if err == nil {
		t.Fatal("quería un error por status 403, no hubo ninguno")
	}
}

func TestGetCostReport_SkipsInvalidLines(t *testing.T) {
	withMockBillingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("esto no es json\n"))
		writeJSONL(w, map[string]any{"BilledCost": 3.0, "ServiceName": "ISR Reads", "Tags": map[string]string{}})
	})

	a := New()
	results, err := a.GetCostReport(context.Background(), adapters.CostReportQuery{
		From: "a", To: "b", Credentials: map[string]string{"token": "tok_test"},
	})
	if err != nil {
		t.Fatalf("una línea inválida no debería tirar todo el reporte: %v", err)
	}
	if len(results) != 1 || results[0].ServiceName != "ISR Reads" {
		t.Fatalf("results = %+v, quería solo la línea válida (ISR Reads)", results)
	}
}
