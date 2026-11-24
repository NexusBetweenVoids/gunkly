package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/scheduler"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/transfer"
	"github.com/NexusBetweenVoids/gunkly/internal/webhook"
)

// registerTransferRoutes: personal and household data export, household
// import. Kept in one block so it merges cleanly with the other route groups.
func (s *Server) registerTransferRoutes() {
	member := household(store.RoleMember)
	upload := int64(limits.ImportMaxBytes)

	s.handle("GET /api/v1/account/export", session.noToken().withStepUp().withRate(rateTransfer), s.exportUser)
	s.handle("GET /api/v1/households/{hh}/export", member.noToken().withRate(rateTransfer), s.exportHousehold)
	s.handle("POST /api/v1/households/import", session.noToken().withRate(rateTransfer).withBodyLimit(upload), s.importNewHousehold)
	s.handle("POST /api/v1/households/{hh}/import", member.noToken().withRate(rateTransfer).withBodyLimit(upload), s.importMerge)
}

// download writes v as a pretty-printed JSON attachment.
func (s *Server) download(w http.ResponseWriter, v any, name string) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+name+`-`+s.now().UTC().Format("2006-01-02")+`.json"`)
	h.Set("Cache-Control", "no-store")
	_, err = w.Write(append(b, '\n'))
	return err
}

func (s *Server) exportUser(w http.ResponseWriter, r *http.Request, c *call) error {
	data, err := s.st.ExportUser(r.Context(), c.user.ID)
	if err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditAccountExport, "", nil)
	return s.download(w, data, "gunkly-user-export")
}

func (s *Server) exportHousehold(w http.ResponseWriter, r *http.Request, c *call) error {
	data, err := s.st.ExportHousehold(r.Context(), c.actor(), c.hh)
	if err != nil {
		return err
	}
	// Webhooks travel only in an owner's export; the signing secret never does.
	// The URLs can carry credentials, so the whole file is sensitive.
	var meta map[string]string
	if role, err := s.st.Role(r.Context(), c.actor(), c.hh); err == nil && role == store.RoleOwner {
		hooks, err := s.st.ExportWebhooks(r.Context(), c.actor(), c.hh)
		if err != nil {
			return err
		}
		for _, x := range hooks {
			u, err := webhook.OpenURL(s.keyring, x.ID, x.URLCiphertext)
			if err != nil {
				return err
			}
			data.Webhooks = append(data.Webhooks, transfer.Webhook{Name: x.Description, Events: x.Events, URL: u, Enabled: x.Enabled})
		}
		if len(hooks) > 0 {
			meta = map[string]string{"webhooks": strconv.Itoa(len(hooks))}
		}
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditHouseholdExport, c.hh, meta)
	return s.download(w, data, "gunkly-household-export")
}

type importSummary struct {
	Household           importHousehold `json:"household"`
	AreasCreated        int             `json:"areasCreated"`
	AreasMerged         int             `json:"areasMerged"`
	ChoresCreated       int             `json:"choresCreated"`
	CompletionsImported int             `json:"completionsImported"`
	Unassigned          int             `json:"unassigned"`
	// Webhooks of the file are re-created disabled with new secrets (owners
	// only, and only while the instance has webhooks on). Each one not created
	// is listed with a reason: webhooks_disabled, owner_only, invalid, limit.
	WebhooksCreated int              `json:"webhooksCreated"`
	WebhooksSkipped []skippedWebhook `json:"webhooksSkipped"`
}

type skippedWebhook struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type importHousehold struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func viewImport(r store.ImportResult, skipped []skippedWebhook) importSummary {
	if skipped == nil {
		skipped = []skippedWebhook{}
	}
	return importSummary{WebhooksCreated: r.WebhooksCreated, WebhooksSkipped: skipped, Household: importHousehold{ID: r.HouseholdID, Name: r.HouseholdName},
		AreasCreated: r.AreasCreated, AreasMerged: r.AreasMerged, ChoresCreated: r.ChoresCreated,
		CompletionsImported: r.CompletionsImported, Unassigned: r.Unassigned}
}

// importOptions resolves the limits an import file is validated against
// (the household's, or the instance's for a new household).
func (s *Server) importOptions(ctx context.Context, hh string) (transfer.Options, error) {
	o := transfer.Options{MaxTotalCompletions: limits.ImportMaxCompletions, ValidPreset: func(n string) bool { _, ok := scheduler.Presets[n]; return ok }}
	for kind, dst := range map[limits.Kind]*int{
		limits.ChoresPerHousehold: &o.MaxChores, limits.AreasPerHousehold: &o.MaxAreas,
		limits.RemindersPerChore: &o.MaxReminders, limits.CompletionsPerChore: &o.MaxCompletions,
	} {
		v, err := s.limits.Limit(ctx, kind, hh)
		if err != nil {
			return o, err
		}
		*dst = v
	}
	return o, nil
}

// prepareWebhooks seals the file's webhooks for import: each gets a fresh
// signing secret and will be stored disabled. Entries that cannot be created
// (webhooks switched off on the instance, importer not an owner, invalid
// events or URL under the egress rules in force now) are reported, not fatal.
// Items that pass here can still hit the household's limit in the store; the
// returned names let the caller report those too.
func (s *Server) prepareWebhooks(ctx context.Context, f *transfer.Household, owner bool) (hooks []store.NewWebhook, names []string, skipped []skippedWebhook) {
	if len(f.Webhooks) == 0 {
		return nil, nil, nil
	}
	reason := ""
	switch {
	case !s.webhooksEnabled(ctx):
		reason = "webhooks_disabled"
	case !owner:
		reason = "owner_only"
	}
	guarded := s.EgressGuardEnabled(ctx)
	for _, w := range f.Webhooks {
		if reason != "" {
			skipped = append(skipped, skippedWebhook{w.Name, reason})
			continue
		}
		p, err := webhook.Prepare(s.keyring, w.URL, w.Name, w.Events, guarded)
		if err != nil || !store.ValidWebhookEvents(w.Events) {
			skipped = append(skipped, skippedWebhook{w.Name, "invalid"})
			continue
		}
		hooks, names = append(hooks, p.NewWebhook), append(names, w.Name)
	}
	return hooks, names, skipped
}

// parseImport reads the (size-capped) upload and validates it.
func (s *Server) parseImport(r *http.Request, hh string) (*transfer.Household, error) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err // *http.MaxBytesError -> 413
	}
	o, err := s.importOptions(r.Context(), hh)
	if err != nil {
		return nil, err
	}
	f, err := transfer.Parse(data, o)
	if err != nil {
		return nil, importError(err)
	}
	return f, nil
}

// importError turns a validation failure into an API error carrying the
// reason code and the field path.
func importError(err error) error {
	var te *transfer.Error
	if errors.As(err, &te) {
		status := http.StatusBadRequest
		if len(te.Reason) > 6 && te.Reason[:6] == "limit_" {
			status = http.StatusConflict
		}
		return &apiError{status: status, code: "import_invalid", reason: te.Reason, path: te.Path}
	}
	var le *store.ImportLimitError
	if errors.As(err, &le) {
		return &apiError{status: http.StatusConflict, code: "import_invalid", reason: le.Reason}
	}
	return err
}

func (s *Server) importNewHousehold(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx := r.Context()
	if err := s.requireAccess(ctx, c.user); err != nil {
		return err
	}
	f, err := s.parseImport(r, "")
	if err != nil {
		return err
	}
	max, err := s.limits.Limit(ctx, limits.HouseholdsPerUser, "")
	if err != nil {
		return err
	}
	hooks, names, skipped := s.prepareWebhooks(ctx, f, true)
	res, err := s.st.ImportNewHousehold(ctx, c.actor(), f, max, hooks)
	if err != nil {
		return importError(err)
	}
	for _, i := range res.WebhooksOverLimit {
		skipped = append(skipped, skippedWebhook{names[i], "limit"})
	}
	s.audit(ctx, c, c.user.ID, store.AuditHouseholdImport, res.HouseholdID, map[string]string{"mode": "new", "webhooks": strconv.Itoa(res.WebhooksCreated)})
	writeJSON(w, http.StatusCreated, viewImport(res, skipped))
	return nil
}

func (s *Server) importMerge(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx := r.Context()
	if err := s.requireAccess(ctx, c.user); err != nil {
		return err
	}
	f, err := s.parseImport(r, c.hh)
	if err != nil {
		return err
	}
	role, err := s.st.Role(ctx, c.actor(), c.hh)
	if err != nil {
		return err
	}
	hooks, names, skipped := s.prepareWebhooks(ctx, f, role == store.RoleOwner)
	res, err := s.st.MergeHousehold(ctx, c.actor(), c.hh, f, hooks)
	if err != nil {
		return importError(err)
	}
	for _, i := range res.WebhooksOverLimit {
		skipped = append(skipped, skippedWebhook{names[i], "limit"})
	}
	s.audit(ctx, c, c.user.ID, store.AuditHouseholdImport, c.hh, map[string]string{"mode": "merge", "webhooks": strconv.Itoa(res.WebhooksCreated)})
	return ok(w, viewImport(res, skipped))
}
