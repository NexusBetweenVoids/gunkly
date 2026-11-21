package api

import (
	_ "embed"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/NexusBetweenVoids/gunkly/internal/i18n"
	"github.com/NexusBetweenVoids/gunkly/internal/ical"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Links that work without signing in: mark-done pages and calendar feeds.
// Both are authenticated only by an unguessable token in the URL, so every
// response is no-store, no cookie is ever set, and the token never reaches a
// log (the access log records r.Pattern, e.g. "POST /done/{token}").

//go:embed done.html
var doneHTML string

//go:embed done.css
var doneCSS []byte

var doneTmpl = template.Must(template.New("done").Parse(doneHTML))

type donePage struct {
	Lang, Title, Heading, Note, Action, Button string
}

// acceptLocale is the first language the browser asks for; i18n falls back
// to the default for anything it does not have. These pages have no session
// and so no user locale.
func acceptLocale(r *http.Request) string {
	first, _, _ := strings.Cut(r.Header.Get("Accept-Language"), ",")
	first, _, _ = strings.Cut(first, ";")
	first = strings.TrimSpace(first)
	if !localeRE.MatchString(first) {
		return ""
	}
	return first
}

// writeDone renders a page. Never cached, never indexed, no cookies.
func writeDone(w http.ResponseWriter, status int, p donePage) error {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	return doneTmpl.Execute(w, p)
}

func (s *Server) doneInvalid(w http.ResponseWriter, r *http.Request, status int) error {
	loc := acceptLocale(r)
	return writeDone(w, status, donePage{Lang: langOf(loc), Title: i18n.T(loc, "done.page_title", nil),
		Heading: i18n.T(loc, "done.invalid_heading", nil), Note: i18n.T(loc, "done.invalid_note", nil)})
}

func langOf(loc string) string {
	if loc == "" {
		return i18n.Default
	}
	return loc
}

// invalidLink reports whether err means "this token does not work", which
// every such cause (unknown, used, expired, stale, guest recipient) shares so
// the page is no oracle. Anything else is a server error.
func invalidLink(err error) bool {
	return errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict) ||
		errors.Is(err, store.ErrInvalid) || errors.Is(err, store.ErrForbidden)
}

func (s *Server) doneFailed(w http.ResponseWriter, r *http.Request, err error) error {
	if invalidLink(err) {
		return s.doneInvalid(w, r, http.StatusNotFound)
	}
	s.log.Error("mark-done failed", "route", infoFrom(r).pattern, "err", err)
	return s.doneInvalid(w, r, http.StatusInternalServerError)
}

// doneConfirm is GET /done/{token}: shows the chore name and a POST button.
// It changes nothing, so link previews and scanners cannot complete chores.
func (s *Server) doneConfirm(w http.ResponseWriter, r *http.Request, _ *call) error {
	token := r.PathValue("token")
	name, err := s.st.PeekMarkDone(r.Context(), token)
	if err != nil {
		return s.doneFailed(w, r, err)
	}
	loc := acceptLocale(r)
	return writeDone(w, http.StatusOK, donePage{Lang: langOf(loc), Title: i18n.T(loc, "done.page_title", nil),
		Heading: i18n.T(loc, "done.confirm_heading", map[string]string{"chore": name}),
		Action:  "/done/" + url.PathEscape(token), Button: i18n.T(loc, "done.confirm_button", nil)})
}

// doneSubmit is POST /done/{token}: completes the chore once and offers a
// 5-minute single-use undo.
func (s *Server) doneSubmit(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx := r.Context()
	res, err := s.st.ConsumeMarkDone(ctx, r.PathValue("token"))
	if err != nil {
		return s.doneFailed(w, r, err)
	}
	meta := map[string]string{"completion": res.CompletionID}
	if res.ChannelID != "" {
		meta["channel"] = res.ChannelID
	}
	s.audit(ctx, c, res.UserID, store.AuditMarkDoneUse, res.HouseholdID, meta)
	loc := acceptLocale(r)
	return writeDone(w, http.StatusOK, donePage{Lang: langOf(loc), Title: i18n.T(loc, "done.page_title", nil),
		Heading: i18n.T(loc, "done.success_heading", map[string]string{"chore": res.ChoreName}),
		Note:    i18n.T(loc, "done.undo_note", nil),
		Action:  "/done/undo/" + url.PathEscape(res.UndoToken), Button: i18n.T(loc, "done.undo_button", nil)})
}

// doneUndo is POST /done/undo/{token}.
func (s *Server) doneUndo(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx := r.Context()
	res, err := s.st.UndoWithToken(ctx, r.PathValue("token"))
	if err != nil {
		return s.doneFailed(w, r, err)
	}
	s.audit(ctx, c, "", store.AuditMarkDoneUndo, res.HouseholdID, map[string]string{"completion": res.CompletionID})
	loc := acceptLocale(r)
	return writeDone(w, http.StatusOK, donePage{Lang: langOf(loc), Title: i18n.T(loc, "done.page_title", nil),
		Heading: i18n.T(loc, "done.undone_heading", nil)})
}

// doneStyle serves the confirmation pages' stylesheet (the CSP forbids inline
// styles).
func (s *Server) doneStyle(w http.ResponseWriter, _ *http.Request, _ *call) error {
	h := w.Header()
	h.Set("Content-Type", "text/css; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=86400")
	_, err := w.Write(doneCSS)
	return err
}

// icalFeed is GET /ical/{token}.ics.
func (s *Server) icalFeed(w http.ResponseWriter, r *http.Request, _ *call) error {
	token, isICS := strings.CutSuffix(r.PathValue("file"), ".ics")
	if !isICS {
		return errNotFound
	}
	f, err := s.st.FeedByToken(r.Context(), token)
	if err != nil {
		return err
	}
	now := s.now()
	body := ical.Render(f, acceptLocale(r), schedule.DateOf(now, f.Location), now)
	h := w.Header()
	h.Set("Content-Type", ical.ContentType)
	h.Set("Cache-Control", "no-store")
	_, err = w.Write(body) //nolint:gosec // G705: text/calendar with nosniff, content escaped by ical.Escape
	return err
}
