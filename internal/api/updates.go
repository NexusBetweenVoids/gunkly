package api

import (
	"net/http"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/updates"
)

type updatesView struct {
	Running   string     `json:"running"`
	Latest    string     `json:"latest"`
	Newer     bool       `json:"newer"`
	Ahead     bool       `json:"ahead"`     // running is newer than latest: the cache is stale or this is a pre-release
	Changelog string     `json:"changelog"` // release notes as plain text; render as text only
	CheckedAt *time.Time `json:"checkedAt"`
	Enabled   bool       `json:"enabled"`
	Checking  bool       `json:"checking"` // a refresh from GitHub is running; ask again shortly
}

// updatesState builds the view from the cache and, when asked or when the
// cache is stale, starts a non-blocking refresh. With the check switched off
// nothing is shown and GitHub is never asked.
func (s *Server) updatesState(r *http.Request, force bool) (updatesView, error) {
	v := updatesView{Running: s.version}
	st, err := s.st.InstanceSettings(r.Context())
	if err != nil {
		return v, err
	}
	if v.Enabled = st.UpdateCheck; !v.Enabled {
		return v, nil
	}
	cache, err := s.st.UpdateCache(r.Context())
	if err != nil {
		return v, err
	}
	v.Latest, v.Changelog = cache.Latest, cache.Changelog
	v.Newer = updates.Newer(s.version, cache.Latest)
	v.Ahead = cache.Latest != "" && updates.Ahead(s.version, cache.Latest)
	if !cache.CheckedAt.IsZero() {
		v.CheckedAt = &cache.CheckedAt
	}
	if s.updates != nil {
		v.Checking = s.updates.Refresh(cache, s.version, force)
	}
	return v, nil
}

// adminUpdates reports the running version and the cached result of the
// release check (internal/updates) at once. If the cache is missing, older
// than an hour or older than the running version it also starts a refresh in
// the background (checking is true until it finishes).
func (s *Server) adminUpdates(w http.ResponseWriter, r *http.Request, _ *call) error {
	v, err := s.updatesState(r, false)
	if err != nil {
		return err
	}
	return ok(w, v)
}

// adminUpdatesCheck is "Check now": it starts a refresh unless one ran in the
// last five minutes and returns the current state.
func (s *Server) adminUpdatesCheck(w http.ResponseWriter, r *http.Request, _ *call) error {
	v, err := s.updatesState(r, true)
	if err != nil {
		return err
	}
	return ok(w, v)
}
