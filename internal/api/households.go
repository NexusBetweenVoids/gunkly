package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

type quietView struct {
	Start int `json:"start"` // minutes after local midnight
	End   int `json:"end"`
}

type householdView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Timezone   string     `json:"timezone"`
	QuietHours *quietView `json:"quietHours"`
	// DefaultReminderPreset applies to chores without their own ("" = none).
	DefaultReminderPreset string `json:"defaultReminderPreset"`
	// AreasEnabled: chores are grouped by area (off by default).
	AreasEnabled bool       `json:"areasEnabled"`
	Role         store.Role `json:"role"`
	CreatedAt    time.Time  `json:"createdAt"`
}

func viewHousehold(h store.Household) householdView {
	v := householdView{ID: h.ID, Name: h.Name, Timezone: h.Timezone, Role: h.Role, CreatedAt: h.CreatedAt,
		DefaultReminderPreset: h.DefaultReminderPreset, AreasEnabled: h.AreasEnabled}
	if h.QuietStartMin != nil && h.QuietEndMin != nil {
		v.QuietHours = &quietView{Start: *h.QuietStartMin, End: *h.QuietEndMin}
	}
	return v
}

func (s *Server) listHouseholds(w http.ResponseWriter, r *http.Request, c *call) error {
	hs, err := s.st.ListHouseholds(r.Context(), c.actor())
	if err != nil {
		return err
	}
	out := make([]householdView, 0, len(hs))
	for _, h := range hs {
		if c.tok != nil { // a token sees only the households it is bound to
			if _, bound := c.tok.Household(h.ID); !bound {
				continue
			}
		}
		out = append(out, viewHousehold(h))
	}
	return ok(w, out)
}

func (s *Server) createHousehold(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Name         string `json:"name"`
		Timezone     string `json:"timezone"`
		AreasEnabled bool   `json:"areasEnabled"` // turn the areas feature on (default off)
		// StarterAreas also creates the starter areas, named in the user's
		// language; ignored unless AreasEnabled is true.
		StarterAreas bool `json:"starterAreas"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if !validName(in.Name) {
		return errInvalid
	}
	ctx := r.Context()
	if err := s.requireAccess(ctx, c.user); err != nil {
		return err
	}
	max, err := s.limits.Limit(ctx, limits.HouseholdsPerUser, "")
	if err != nil {
		return err
	}
	nh := store.NewHousehold{Name: in.Name, Timezone: in.Timezone, MaxHouseholds: max}
	nh.AreasEnabled = in.AreasEnabled
	if in.AreasEnabled && in.StarterAreas {
		nh.StarterAreas = starterAreas(c.user.Locale)
	}
	h, err := s.st.CreateHousehold(ctx, c.actor(), nh)
	if err != nil {
		return err
	}
	s.audit(ctx, c, c.user.ID, store.AuditHouseholdCreate, h.ID, nil)
	writeJSON(w, http.StatusCreated, viewHousehold(h))
	return nil
}

func (s *Server) getHousehold(w http.ResponseWriter, r *http.Request, c *call) error {
	h, err := s.st.Household(r.Context(), c.actor(), c.hh)
	if err != nil {
		return err
	}
	return ok(w, viewHousehold(h))
}

func (s *Server) patchHousehold(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Name       *string             `json:"name"`
		Timezone   *string             `json:"timezone"`
		QuietHours optional[quietView] `json:"quietHours"`
		// DefaultReminderPreset is "" (none), "gentle", "nag" or "none".
		DefaultReminderPreset *string `json:"defaultReminderPreset"`
		AreasEnabled          *bool   `json:"areasEnabled"`
		// StarterAreas true creates the starter areas when the household has none.
		StarterAreas bool `json:"starterAreas"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Name != nil && !validName(*in.Name) {
		return errInvalid
	}
	if in.DefaultReminderPreset != nil && !validPreset(*in.DefaultReminderPreset) {
		return errInvalid
	}
	u := store.HouseholdUpdate{Name: in.Name, Timezone: in.Timezone, DefaultReminderPreset: in.DefaultReminderPreset, AreasEnabled: in.AreasEnabled}
	if in.StarterAreas {
		u.StarterAreas = starterAreas(c.user.Locale)
	}
	if in.QuietHours.set {
		var q *store.QuietHours
		if v := in.QuietHours.Value; v != nil {
			q = &store.QuietHours{StartMin: v.Start, EndMin: v.End}
		}
		u.QuietHours = &q
	}
	h, err := s.st.UpdateHousehold(r.Context(), c.actor(), c.hh, u)
	if err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditHouseholdSettings, c.hh, nil)
	return ok(w, viewHousehold(h))
}

type memberView struct {
	UserID      string     `json:"userId"`
	DisplayName string     `json:"displayName"`
	Role        store.Role `json:"role"`
	JoinedAt    time.Time  `json:"joinedAt"`
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, c *call) error {
	ms, err := s.st.ListMembers(r.Context(), c.actor(), c.hh)
	if err != nil {
		return err
	}
	out := make([]memberView, 0, len(ms))
	for _, m := range ms {
		out = append(out, memberView{UserID: m.UserID, DisplayName: m.DisplayName, Role: m.Role, JoinedAt: m.JoinedAt})
	}
	return ok(w, out)
}

func (s *Server) changeRole(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Role store.Role `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	target := r.PathValue("user")
	if err := s.st.ChangeRole(r.Context(), c.actor(), c.hh, target, in.Role); err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditMemberRoleChange, c.hh, map[string]string{"user": target, "role": string(in.Role)})
	return noContent(w)
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request, c *call) error {
	target := r.PathValue("user")
	if target == c.user.ID {
		return s.leaveHousehold(w, r, c)
	}
	// The store allows owners to remove anyone and members to remove themselves.
	if err := s.st.RemoveMember(r.Context(), c.actor(), c.hh, target); err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditMemberRemove, c.hh, map[string]string{"user": target})
	return noContent(w)
}

// Invite link lifetime bounds.
const (
	inviteDefaultTTL = 7 * 24 * time.Hour
	inviteMaxTTL     = 30 * 24 * time.Hour
)

type inviteView struct {
	ID        string     `json:"id"`
	Role      store.Role `json:"role"`
	CreatedBy string     `json:"createdBy,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	MaxUses   int        `json:"maxUses"`
	Uses      int        `json:"uses"`
	Token     string     `json:"token,omitempty"` // only in the create response
	URL       string     `json:"url,omitempty"`   // only in the create response
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Role           store.Role `json:"role"`
		ExpiresInHours int        `json:"expiresInHours"`
		MaxUses        int        `json:"maxUses"` // 1..100, default 1
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	ttl := time.Duration(in.ExpiresInHours) * time.Hour
	switch {
	case in.ExpiresInHours == 0:
		ttl = inviteDefaultTTL
	case in.ExpiresInHours < 0 || ttl > inviteMaxTTL:
		return errInvalid
	}
	if in.MaxUses == 0 {
		in.MaxUses = 1
	}
	if in.MaxUses < 1 || in.MaxUses > store.MaxInviteUses {
		return errInvalid
	}
	ctx := r.Context()
	maxActive, err := s.limits.Limit(ctx, limits.ActiveInvitesPerHousehold, c.hh)
	if err != nil {
		return err
	}
	token := ids.Token()
	inv, err := s.st.CreateInvite(ctx, c.actor(), c.hh, store.NewInvite{
		Role: in.Role, TokenHash: ids.Hash(token), ExpiresAt: s.now().Add(ttl), MaxActive: maxActive, MaxUses: in.MaxUses,
	})
	if err != nil {
		return err
	}
	s.audit(ctx, c, c.user.ID, store.AuditInviteCreate, c.hh, map[string]string{"invite": inv.ID, "role": string(inv.Role)})
	v := viewInvite(inv)
	v.Token = token
	if s.baseURL != "" {
		v.URL = s.baseURL + "/invite/" + token
	}
	writeJSON(w, http.StatusCreated, v)
	return nil
}

func viewInvite(i store.Invite) inviteView {
	return inviteView{ID: i.ID, Role: i.Role, CreatedBy: i.CreatedBy, CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt, MaxUses: i.MaxUses, Uses: i.Uses}
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request, c *call) error {
	invs, err := s.st.ListInvites(r.Context(), c.actor(), c.hh)
	if err != nil {
		return err
	}
	out := make([]inviteView, 0, len(invs))
	for _, i := range invs {
		out = append(out, viewInvite(i))
	}
	return ok(w, out)
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request, c *call) error {
	id := r.PathValue("invite")
	if err := s.st.RevokeInvite(r.Context(), c.actor(), c.hh, id); err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditInviteRevoke, c.hh, map[string]string{"invite": id})
	return noContent(w)
}

// inviteTarget validates the path token and returns its hash and the
// limits that apply to joining its household.
func (s *Server) inviteTarget(ctx context.Context, r *http.Request) (string, store.JoinLimits, error) {
	token := r.PathValue("token")
	if token == "" || len(token) > 128 {
		return "", store.JoinLimits{}, errNotFound
	}
	hash := ids.Hash(token)
	hh, err := s.st.InviteHousehold(ctx, hash)
	if err != nil {
		return "", store.JoinLimits{}, err
	}
	var lim store.JoinLimits
	if lim.MaxMembers, err = s.limits.Limit(ctx, limits.MembersPerHousehold, hh); err != nil {
		return "", lim, err
	}
	if lim.MaxHouseholds, err = s.limits.Limit(ctx, limits.HouseholdsPerUser, ""); err != nil {
		return "", lim, err
	}
	return hash, lim, nil
}

type joinedView struct {
	HouseholdID string     `json:"householdId"`
	Role        store.Role `json:"role"`
}

func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx := r.Context()
	if err := s.requireAccess(ctx, c.user); err != nil {
		return err
	}
	hash, lim, err := s.inviteTarget(ctx, r)
	if err != nil {
		return err
	}
	j, err := s.st.AcceptInvite(ctx, c.user.ID, hash, lim)
	if err != nil {
		return err
	}
	s.audit(ctx, c, c.user.ID, store.AuditInviteAccept, j.HouseholdID, nil)
	return ok(w, joinedView{HouseholdID: j.HouseholdID, Role: j.Role})
}

type newAccount struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}

// validate checks the fields and returns the store input with the password
// hashed.
func (s *Server) validate(ctx context.Context, a newAccount) (store.NewUser, error) {
	if !validName(a.DisplayName) || !usernameRE.MatchString(a.Username) || (a.Email != "" && !emailRE.MatchString(a.Email)) {
		return store.NewUser{}, errInvalid
	}
	if err := s.checkNewPassword(ctx, a.Password); err != nil {
		return store.NewUser{}, err
	}
	return store.NewUser{Username: a.Username, Email: a.Email, DisplayName: a.DisplayName, PasswordHash: s.hashPassword(a.Password)}, nil
}

func (s *Server) registerWithInvite(w http.ResponseWriter, r *http.Request, c *call) error {
	var in newAccount
	if err := decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	st, err := s.st.InstanceSettings(ctx)
	if err != nil {
		return err
	}
	if !st.OpenSignup && !st.InviteRegistration {
		return errf(http.StatusForbidden, "registration_disabled")
	}
	hash, lim, err := s.inviteTarget(ctx, r)
	if err != nil {
		return err
	}
	nu, err := s.validate(ctx, in)
	if err != nil {
		return err
	}
	u, j, err := s.st.RegisterWithInvite(ctx, nu, hash, lim)
	if errors.Is(err, store.ErrConflict) {
		// Same answer whether the username or the email exists: no oracle.
		return errf(http.StatusBadRequest, "registration_failed")
	} else if err != nil {
		return err
	}
	if err := s.startSession(w, r, c, u, http.StatusCreated); err != nil {
		return err
	}
	s.audit(ctx, c, u.ID, store.AuditRegisterByInvite, j.HouseholdID, nil)
	s.verifyNewAccount(ctx, c, u)
	return nil
}
