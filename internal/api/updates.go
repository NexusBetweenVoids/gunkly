package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
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
	// CanUpdate: an update webhook is configured, so "Update now" can ask the
	// admin's updater to pull the new image and restart Gunkly.
	CanUpdate bool `json:"canUpdate"`
}

// updatesState builds the view from the cache and, when asked or when the
// cache is stale, starts a non-blocking refresh. With the check switched off
// nothing is shown and GitHub is never asked.
func (s *Server) updatesState(r *http.Request, force bool) (updatesView, error) {
	v := updatesView{Running: s.version}
	if cfg, err := s.config(r.Context(), false); err == nil {
		v.CanUpdate = cfg.str("updates.webhook_url") != ""
	}
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

// updateHookTimeout bounds the call to the updater. Some updaters answer only
// when they are done, and that may include restarting this very server, so
// no answer in time is reported as "sent", not as a failure.
const updateHookTimeout = 15 * time.Second

// adminApplyUpdate calls the configured update webhook once: POST with an
// empty body and, when a token is set, "Authorization: Bearer <token>".
// For Dockhand the URL is its stack deploy API (/api/stacks/<name>/deploy) and
// the body {"pull":true} makes it pull the newest images before recreating.
// Gunkly never touches the container runtime itself.
func (s *Server) adminApplyUpdate(w http.ResponseWriter, r *http.Request, c *call) error {
	cfg, err := s.config(r.Context(), true)
	if err != nil {
		return err
	}
	target := cfg.str("updates.webhook_url")
	if target == "" || !validUpdateURL(target) {
		return errf(http.StatusConflict, "update_webhook_missing")
	}
	ctx, cancel := context.WithTimeout(r.Context(), updateHookTimeout)
	defer cancel()
	var body io.Reader
	dockhand := cfg.str("updates.webhook_kind") == "dockhand"
	if dockhand {
		body = strings.NewReader(`{"pull":true}`)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return errf(http.StatusConflict, "update_webhook_missing")
	}
	if dockhand {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "gunkly/"+s.version)
	if tok := cfg.str("updates.webhook_token"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditAdminUpdate, "", map[string]string{"from": s.version, "updater": cfg.str("updates.webhook_kind")})
	out := map[string]any{}
	resp, err := s.updateHTTP.Do(req)
	switch {
	case err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)):
		out["result"] = "sent" // the updater is busy (or already restarting us)
	case err != nil:
		s.log.Warn("update webhook failed", "err", err)
		out["result"], out["error"] = "failed", "unreachable"
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			out["result"] = "accepted"
		} else {
			out["result"], out["error"], out["status"] = "failed", "rejected", resp.StatusCode
		}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
