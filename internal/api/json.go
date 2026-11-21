package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// apiError is an error with a stable machine-readable code; the UI
// translates codes, the server never sends user-facing text.
type apiError struct {
	status int
	code   string
}

func (e *apiError) Error() string { return e.code }

func errf(status int, code string) error { return &apiError{status: status, code: code} }

var (
	errCSRF               = errf(http.StatusForbidden, "csrf_failed")
	errUnauthenticated    = errf(http.StatusUnauthorized, "unauthenticated")
	errForbidden          = errf(http.StatusForbidden, "forbidden")
	errStepUp             = errf(http.StatusForbidden, "step_up_required")
	errInvalidCredentials = errf(http.StatusUnauthorized, "invalid_credentials")
	errInvalidCode        = errf(http.StatusUnauthorized, "invalid_code")
	errNotFound           = errf(http.StatusNotFound, "not_found")
	errInvalid            = errf(http.StatusBadRequest, "invalid")
)

// fail maps err to a JSON error response. Unexpected errors are logged
// (without request data) and reported generically.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &ae):
		writeError(w, ae.status, ae.code)
	case errors.As(err, &mbe):
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid")
	case errors.Is(err, store.ErrLimit), errors.Is(err, limits.ErrExceeded):
		writeError(w, http.StatusConflict, "limit_reached")
	default:
		s.log.Error("request failed", "route", infoFrom(r).pattern, "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any) error {
	writeJSON(w, http.StatusOK, v)
	return nil
}

func noContent(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// decode reads exactly one JSON object into v, rejecting unknown fields and
// trailing data.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return err
		}
		return errf(http.StatusBadRequest, "invalid_json")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errf(http.StatusBadRequest, "invalid_json")
	}
	return nil
}

// optional distinguishes an absent JSON field (set=false) from null
// (set=true, Value=nil).
type optional[T any] struct {
	set   bool
	Value *T
}

func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	o.Value = new(T)
	return json.Unmarshal(b, o.Value)
}
