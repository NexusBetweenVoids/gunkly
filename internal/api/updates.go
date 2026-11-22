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
	Changelog string     `json:"changelog"` // release notes as plain text; render as text only
	CheckedAt *time.Time `json:"checkedAt"`
	Enabled   bool       `json:"enabled"`
}

// adminUpdates reports the running version and the cached result of the
// background release check (internal/updates). It never triggers a request
// itself. With the check switched off nothing cached is shown.
func (s *Server) adminUpdates(w http.ResponseWriter, r *http.Request, _ *call) error {
	v := updatesView{Running: s.version}
	st, err := s.st.InstanceSettings(r.Context())
	if err != nil {
		return err
	}
	if v.Enabled = st.UpdateCheck; v.Enabled {
		cache, err := s.st.UpdateCache(r.Context())
		if err != nil {
			return err
		}
		v.Latest, v.Changelog = cache.Latest, cache.Changelog
		v.Newer = updates.Newer(s.version, cache.Latest)
		if !cache.CheckedAt.IsZero() {
			v.CheckedAt = &cache.CheckedAt
		}
	}
	return ok(w, v)
}
