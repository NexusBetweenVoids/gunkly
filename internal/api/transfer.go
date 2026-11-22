package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/scheduler"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/transfer"
)

// registerTransferRoutes: personal and household data export, household
// import. Kept in one block so it merges cleanly with the other route groups.
func (s *Server) registerTransferRoutes() {
	member := household(store.RoleMember)
	upload := int64(limits.ImportMaxBytes)

	s.handle("GET /api/v1/account/export", session.withStepUp().withRate(rateTransfer), s.exportUser)
	s.handle("GET /api/v1/households/{hh}/export", member.withRate(rateTransfer), s.exportHousehold)
	s.handle("POST /api/v1/households/import", session.withRate(rateTransfer).withBodyLimit(upload), s.importNewHousehold)
	s.handle("POST /api/v1/households/{hh}/import", member.withRate(rateTransfer).withBodyLimit(upload), s.importMerge)
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
	s.audit(r.Context(), c, c.user.ID, store.AuditHouseholdExport, c.hh, nil)
	return s.download(w, data, "gunkly-household-export")
}

type importSummary struct {
	Household           importHousehold `json:"household"`
	AreasCreated        int             `json:"areasCreated"`
	AreasMerged         int             `json:"areasMerged"`
	ChoresCreated       int             `json:"choresCreated"`
	CompletionsImported int             `json:"completionsImported"`
	Unassigned          int             `json:"unassigned"`
}

type importHousehold struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func viewImport(r store.ImportResult) importSummary {
	return importSummary{Household: importHousehold{ID: r.HouseholdID, Name: r.HouseholdName},
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
	res, err := s.st.ImportNewHousehold(ctx, c.actor(), f, max)
	if err != nil {
		return importError(err)
	}
	s.audit(ctx, c, c.user.ID, store.AuditHouseholdImport, res.HouseholdID, map[string]string{"mode": "new"})
	writeJSON(w, http.StatusCreated, viewImport(res))
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
	res, err := s.st.MergeHousehold(ctx, c.actor(), c.hh, f)
	if err != nil {
		return importError(err)
	}
	s.audit(ctx, c, c.user.ID, store.AuditHouseholdImport, c.hh, map[string]string{"mode": "merge"})
	return ok(w, viewImport(res))
}
