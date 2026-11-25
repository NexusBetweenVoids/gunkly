package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/NexusBetweenVoids/gunkly/internal/metrics"
)

// metricsAuthorized checks "Authorization: Bearer <token>" against the
// configured token in constant time (both sides hashed first, so neither the
// content nor the length of the token leaks through timing).
func (s *Server) metricsAuthorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return false
	}
	got := sha256.Sum256([]byte(h[len(prefix):]))
	rt := s.cur()
	return rt.metricsOn && subtle.ConstantTimeCompare(got[:], rt.metricsHash[:]) == 1
}

// metricsHandler serves the Prometheus text exposition. No per-user or
// per-household series exist (internal/metrics).
func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request, _ *call) error {
	w.Header().Set("Content-Type", metrics.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	return s.metrics.Write(r.Context(), w)
}
