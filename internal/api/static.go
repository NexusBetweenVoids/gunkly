package api

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// serveStatic serves the embedded SPA: the exact file if it exists, else
// index.html (history-mode routing). Hashed /assets/* are immutable.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, _ *call) error {
	if s.static == nil || strings.HasPrefix(r.URL.Path, "/api/") {
		return errNotFound
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	// No app route starts with a dot: /.git/config, /.env and unknown
	// /.well-known/* paths are real 404s, not the app shell.
	for seg := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(seg, ".") {
			return errNotFound
		}
	}
	f, err := s.static.Open(name)
	if err == nil {
		if st, serr := f.Stat(); serr != nil || st.IsDir() {
			_ = f.Close()
			f, err = nil, fs.ErrNotExist
		}
	}
	if err != nil {
		if strings.HasPrefix(name, "assets/") {
			return errNotFound // a missing hashed asset is a real 404, not an app route
		}
		name = "index.html"
		if f, err = s.static.Open(name); err != nil {
			return errNotFound
		}
	}
	defer f.Close()
	rs, isSeeker := f.(io.ReadSeeker)
	if !isSeeker {
		return errNotFound
	}
	h := w.Header()
	switch {
	case strings.HasPrefix(name, "assets/"):
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	case name == "index.html" || name == "sw.js" || name == "manifest.webmanifest":
		h.Set("Cache-Control", "no-cache")
	default:
		h.Set("Cache-Control", "public, max-age=86400")
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if name == "manifest.webmanifest" {
		ct = "application/manifest+json"
	}
	if ct != "" {
		h.Set("Content-Type", ct)
	}
	http.ServeContent(w, r, name, time.Time{}, rs)
	return nil
}
