package api

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
)

// Sub-path hosting: GUNKLY_BASE_URL may carry a path ("https://host/gunkly").
// Everything is then served under that prefix and nowhere else. Handler
// strips the prefix before routing, so every route pattern, handler and test
// stays prefix-free; whatever leaves the server (links, cookies, redirects,
// metadata, templates) goes back through s.path.

// basePath is the path prefix of a base URL: "" for none, else "/gunkly".
func basePath(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(u.EscapedPath(), "/")
}

// path turns an app path ("/signin") into the path a browser must use.
func (s *Server) path(p string) string { return s.prefix + p }

// cookiePath is the Path attribute of the server's cookies: the prefix, so
// two apps on one host do not share cookies, or "/".
func (s *Server) cookiePath() string {
	if s.prefix == "" {
		return "/"
	}
	return s.prefix
}

// Handler returns the root handler with all middleware applied.
func (s *Server) Handler() http.Handler {
	h := s.middleware(s.mux)
	if s.prefix == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == s.prefix || p == "/":
			s.redirectTo(w, r, s.prefix+"/")
		case strings.HasPrefix(p, s.prefix+"/"):
			s.serveStripped(h, w, r, strings.TrimPrefix(p, s.prefix))
		default:
			if inner, ok := s.wellKnownAtRoot(p); ok {
				s.serveStripped(h, w, r, inner)
				return
			}
			writeError(w, http.StatusNotFound, "not_found")
		}
	})
}

func (s *Server) redirectTo(w http.ResponseWriter, r *http.Request, to string) {
	if r.URL.RawQuery != "" {
		to += "?" + r.URL.RawQuery
	}
	w.Header().Set("Location", to)
	w.WriteHeader(http.StatusTemporaryRedirect)
}

func (s *Server) serveStripped(h http.Handler, w http.ResponseWriter, r *http.Request, inner string) {
	r2 := new(http.Request)
	*r2 = *r
	u := *r.URL
	u.Path, u.RawPath = inner, ""
	r2.URL = &u
	h.ServeHTTP(w, r2)
}

// wellKnownAtRoot maps the RFC 8414 / RFC 9728 discovery URLs of a host that
// serves Gunkly under a prefix ("/.well-known/oauth-authorization-server/gunkly",
// "/.well-known/oauth-protected-resource/gunkly/mcp": the well-known segment
// goes between host and path) to the routes that answer them. A reverse proxy
// that forwards these two paths makes spec-following clients find the server.
func (s *Server) wellKnownAtRoot(p string) (string, bool) {
	if rest, ok := strings.CutPrefix(p, "/.well-known/oauth-authorization-server"); ok && rest == s.prefix {
		return "/.well-known/oauth-authorization-server", true
	}
	if rest, ok := strings.CutPrefix(p, "/.well-known/oauth-protected-resource"); ok && rest == s.prefix+"/mcp" {
		return "/.well-known/oauth-protected-resource/mcp", true
	}
	return "", false
}

// rewriteShell fills in the prefix of the built front end. Vite builds with a
// relative base ("./assets/..."), so one bundle serves any prefix: index.html
// gets absolute prefixed URLs plus the meta tag the router and API client
// read, and the manifest's paths are prefixed. Both are computed once.
func (s *Server) rewriteShell(name string, b []byte) []byte {
	switch name {
	case "index.html":
		b = bytes.ReplaceAll(b, []byte(`="/`), []byte(`="`+s.prefix+`/`))
		b = bytes.ReplaceAll(b, []byte(`="./`), []byte(`="`+s.prefix+`/`))
		if s.prefix != "" {
			b = bytes.Replace(b, []byte("</head>"), []byte(`<meta name="gunkly-base" content="`+s.prefix+`" /></head>`), 1)
		}
	case "manifest.webmanifest":
		b = bytes.ReplaceAll(b, []byte(`"/`), []byte(`"`+s.prefix+`/`))
	}
	return b
}
