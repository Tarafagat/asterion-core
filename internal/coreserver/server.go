// Package coreserver expone el Registry de adapters como un servicio HTTP.
// Este es el único proceso que conoce los adapters de proveedor: el CLI y
// la API de Asterion (FastAPI/Python) le hablan a este servicio en vez de
// implementar cada uno su propia integración con AWS/Azure/GCP/OCI.
package coreserver

import (
	"context"
	"encoding/json"
	"net/http"

	"asterion-core/internal/adapters"
	"asterion-core/internal/capabilities"
)

// Server expone un adapters.Registry como una API HTTP. Implementa
// http.Handler, así que se sirve con http.ListenAndServe directo (ver
// cmd/asterion-core/main.go).
//
// Rutas:
//
//	GET  /health
//	GET  /providers
//	GET  /providers/{code}/capabilities
//	POST /adapters/{code}/instances
//	POST /adapters/{code}/networks
//	POST /adapters/{code}/databases
//	POST /adapters/{code}/buckets
//	POST /adapters/{code}/instances/discover
//	POST /adapters/{code}/networks/discover
//	POST /adapters/{code}/databases/discover
//	POST /adapters/{code}/buckets/discover
type Server struct {
	registry *adapters.Registry
	mux      *http.ServeMux
}

// New arma un Server listo para servir sobre el Registry dado.
func New(registry *adapters.Registry) *Server {
	s := &Server{registry: registry, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /providers", s.handleListProviders)
	s.mux.HandleFunc("GET /providers/{code}/capabilities", s.handleCapabilities)
	s.mux.HandleFunc("POST /adapters/{code}/instances", s.handleCreateInstance)
	s.mux.HandleFunc("POST /adapters/{code}/networks", s.handleCreateNetwork)
	s.mux.HandleFunc("POST /adapters/{code}/databases", s.handleCreateDatabase)
	s.mux.HandleFunc("POST /adapters/{code}/buckets", s.handleCreateBucket)
	s.mux.HandleFunc("POST /adapters/{code}/instances/discover", s.handleListInstances)
	s.mux.HandleFunc("POST /adapters/{code}/networks/discover", s.handleListNetworks)
	s.mux.HandleFunc("POST /adapters/{code}/databases/discover", s.handleListManagedDatabases)
	s.mux.HandleFunc("POST /adapters/{code}/buckets/discover", s.handleListBuckets)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.registry.Codes()})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	adapter, err := s.registry.Get(code)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	set := adapter.Capabilities()
	out := make(map[string]bool, len(capabilities.All))
	for _, c := range capabilities.All {
		out[string(c)] = set.Has(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": code, "capabilities": out})
}

func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	adapter, err := s.resolveWithCapability(w, code, capabilities.Compute)
	if err != nil {
		return
	}
	var spec adapters.InstanceSpec
	if !decodeBody(w, r, &spec) {
		return
	}
	result, err := adapter.CreateInstance(context.Background(), spec)
	respondAdapterResult(w, result, err)
}

func (s *Server) handleCreateNetwork(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	adapter, err := s.resolveWithCapability(w, code, capabilities.Network)
	if err != nil {
		return
	}
	var spec adapters.NetworkSpec
	if !decodeBody(w, r, &spec) {
		return
	}
	result, err := adapter.CreateNetwork(context.Background(), spec)
	respondAdapterResult(w, result, err)
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	adapter, err := s.resolveWithCapability(w, code, capabilities.Database)
	if err != nil {
		return
	}
	var spec adapters.DatabaseSpec
	if !decodeBody(w, r, &spec) {
		return
	}
	result, err := adapter.CreateManagedDatabase(context.Background(), spec)
	respondAdapterResult(w, result, err)
}

func (s *Server) handleCreateBucket(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	adapter, err := s.resolveWithCapability(w, code, capabilities.Storage)
	if err != nil {
		return
	}
	var spec adapters.BucketSpec
	if !decodeBody(w, r, &spec) {
		return
	}
	result, err := adapter.CreateBucket(context.Background(), spec)
	respondAdapterResult(w, result, err)
}

func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	adapter, err := s.resolveWithCapability(w, code, capabilities.Discovery)
	if err != nil {
		return
	}
	var q adapters.DiscoveryQuery
	if !decodeBody(w, r, &q) {
		return
	}
	result, err := adapter.ListInstances(context.Background(), q)
	respondListResult(w, result, err)
}

func (s *Server) handleListNetworks(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	adapter, err := s.resolveWithCapability(w, code, capabilities.Discovery)
	if err != nil {
		return
	}
	var q adapters.DiscoveryQuery
	if !decodeBody(w, r, &q) {
		return
	}
	result, err := adapter.ListNetworks(context.Background(), q)
	respondListResult(w, result, err)
}

func (s *Server) handleListManagedDatabases(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	adapter, err := s.resolveWithCapability(w, code, capabilities.Discovery)
	if err != nil {
		return
	}
	var q adapters.DiscoveryQuery
	if !decodeBody(w, r, &q) {
		return
	}
	result, err := adapter.ListManagedDatabases(context.Background(), q)
	respondListResult(w, result, err)
}

func (s *Server) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	adapter, err := s.resolveWithCapability(w, code, capabilities.Discovery)
	if err != nil {
		return
	}
	var q adapters.DiscoveryQuery
	if !decodeBody(w, r, &q) {
		return
	}
	result, err := adapter.ListBuckets(context.Background(), q)
	respondListResult(w, result, err)
}

func (s *Server) resolveWithCapability(w http.ResponseWriter, code string, cap capabilities.Capability) (adapters.ProviderAdapter, error) {
	adapter, err := s.registry.Get(code)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return nil, err
	}
	if err := adapters.RequireCapability(adapter, cap); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return nil, err
	}
	return adapter, nil
}

func respondAdapterResult[T any](w http.ResponseWriter, result T, err error) {
	if err != nil {
		if err == adapters.ErrNotImplemented {
			writeError(w, http.StatusNotImplemented, err)
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// respondListResult es como respondAdapterResult pero para operaciones de
// descubrimiento (solo lectura): responde 200, no 201, cuando hay resultado.
func respondListResult[T any](w http.ResponseWriter, result []T, err error) {
	if err != nil {
		if err == adapters.ErrNotImplemented {
			writeError(w, http.StatusNotImplemented, err)
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
