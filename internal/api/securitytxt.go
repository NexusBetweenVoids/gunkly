package api

import (
	"net/http"
	"strings"
	"time"
)

// securityTxtValidity is how far ahead Expires lies. RFC 9116 asks for less
// than a year; the file is generated per request, so it never goes stale.
const securityTxtValidity = 180 * 24 * time.Hour

// securityTxt serves /.well-known/security.txt (RFC 9116) when the operator
// set GUNKLY_SECURITY_CONTACT; otherwise the path does not exist.
func (s *Server) securityTxt(w http.ResponseWriter, _ *http.Request, _ *call) error {
	if s.secContact == "" {
		return errNotFound
	}
	var b strings.Builder
	b.WriteString("Contact: " + s.secContact + "\n")
	b.WriteString("Expires: " + s.now().Add(securityTxtValidity).UTC().Truncate(24*time.Hour).Format(time.RFC3339) + "\n")
	if s.secPolicy != "" {
		b.WriteString("Policy: " + s.secPolicy + "\n")
	}
	b.WriteString("Preferred-Languages: en\n")
	if s.baseURL != "" {
		b.WriteString("Canonical: " + s.baseURL + "/.well-known/security.txt\n")
	}
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(b.String()))
	return nil
}
