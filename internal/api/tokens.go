package api

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// API tokens (ADR 0014): "gk_" + 43 base64url characters, stored only as a
// SHA-256 hash, sent as "Authorization: Bearer gk_...".

const (
	tokenPrefix = "gk_"
	tokenLen    = len(tokenPrefix) + 43
	maxTokenHH  = 50
)

var (
	errTokenNotAllowed   = errf(http.StatusForbidden, "token_not_allowed")
	errInsufficientScope = errf(http.StatusForbidden, "insufficient_scope")
)

// tokenScopes is the display and storage order of the scopes.
var tokenScopes = []string{store.ScopeRead, store.ScopeComplete, store.ScopeWrite}

// bearerFrom returns the secret of an "Authorization: Bearer" header.
func bearerFrom(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

// failBearer answers a missing, malformed, unknown, expired or revoked token
// (all alike: no oracle) with 401.
func (s *Server) failBearer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	s.fail(w, r, errUnauthenticated)
}

// authenticateBearer authenticates r by its API token, applies the per-IP and
// per-token rate limits and checks the route allows the token's scopes. On
// failure it has written the response and returns false.
func (s *Server) authenticateBearer(w http.ResponseWriter, r *http.Request, c *call, p policy, secret string) bool {
	if !s.allow(w, rateTokenIP+":ip:"+c.rip) {
		return false
	}
	if len(secret) != tokenLen || !strings.HasPrefix(secret, tokenPrefix) {
		s.failBearer(w, r)
		return false
	}
	tok, u, err := s.st.APITokenByHash(r.Context(), ids.Hash(secret))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.failBearer(w, r)
		} else {
			s.fail(w, r, err)
		}
		return false
	}
	if !s.allow(w, rateToken+":tok:"+tok.ID) {
		return false
	}
	switch {
	case p.auth == authBearer:
	case p.token == tokenNone:
		s.fail(w, r, errTokenNotAllowed)
		return false
	case !tok.HasScope(p.token):
		s.fail(w, r, errInsufficientScope)
		return false
	}
	if s.now().Sub(tok.LastUsedAt) >= store.TokenLastUsedEvery {
		if err := s.st.TouchAPIToken(r.Context(), tok.ID, tok.UserID); err != nil {
			s.log.Error("token touch", "err", err)
		}
	}
	c.user, c.tok = u, &tok
	if !safeMethod(r.Method) && !s.allow(w, rateUserWrite+":user:"+u.ID) {
		return false
	}
	return true
}

type tokenHouseholdView struct {
	ID   string     `json:"id"`
	Name string     `json:"name"`
	Role store.Role `json:"role"`
}

func viewTokenHouseholds(hs []store.TokenHousehold) []tokenHouseholdView {
	out := make([]tokenHouseholdView, 0, len(hs))
	for _, h := range hs {
		out = append(out, tokenHouseholdView{ID: h.ID, Name: h.Name, Role: h.Role})
	}
	return out
}

type tokenView struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Scopes     []string             `json:"scopes"`
	Households []tokenHouseholdView `json:"households"`
	CreatedAt  time.Time            `json:"createdAt"`
	LastUsedAt *time.Time           `json:"lastUsedAt"`
	ExpiresAt  *time.Time           `json:"expiresAt"`
	// Token is the secret, present only in the response that creates it.
	Token string `json:"token,omitempty"`
}

func viewToken(t store.APIToken) tokenView {
	return tokenView{ID: t.ID, Name: t.Name, Scopes: t.Scopes, Households: viewTokenHouseholds(t.Households),
		CreatedAt: t.CreatedAt, LastUsedAt: timePtr(t.LastUsedAt), ExpiresAt: timePtr(t.ExpiresAt)}
}

// getToken describes the calling token (bearer only): what an integration
// asks first to learn which households and scopes it has.
func (s *Server) getToken(w http.ResponseWriter, _ *http.Request, c *call) error {
	t := c.tok
	return ok(w, map[string]any{
		"id": t.ID, "name": t.Name, "scopes": t.Scopes, "households": viewTokenHouseholds(t.Households),
		"user":      map[string]string{"id": c.user.ID, "displayName": c.user.DisplayName},
		"expiresAt": timePtr(t.ExpiresAt),
	})
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request, c *call) error {
	ts, err := s.st.ListAPITokens(r.Context(), c.user.ID)
	if err != nil {
		return err
	}
	out := make([]tokenView, 0, len(ts))
	for _, t := range ts {
		out = append(out, viewToken(t))
	}
	return ok(w, out)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		Households    []string `json:"households"`
		ExpiresInDays *int     `json:"expiresInDays"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if !validName(in.Name) || len(in.Scopes) == 0 || len(in.Scopes) > len(tokenScopes) ||
		len(in.Households) == 0 || len(in.Households) > maxTokenHH ||
		in.ExpiresInDays != nil && (*in.ExpiresInDays < 1 || *in.ExpiresInDays > 3650) {
		return errInvalid
	}
	for _, sc := range in.Scopes {
		if !store.ValidScope(sc) {
			return errInvalid
		}
	}
	for _, h := range in.Households {
		if h == "" || utf8.RuneCountInString(h) > 64 {
			return errInvalid
		}
	}
	ctx := r.Context()
	if err := s.requireAccess(ctx, c.user); err != nil {
		return err
	}
	max, err := s.limits.Limit(ctx, limits.APITokensPerUser, "")
	if err != nil {
		return err
	}
	nt := store.NewAPIToken{Name: in.Name, Max: max, HouseholdIDs: slices.Compact(slices.Sorted(slices.Values(in.Households)))}
	for _, sc := range tokenScopes { // canonical order, duplicates dropped
		if slices.Contains(in.Scopes, sc) {
			nt.Scopes = append(nt.Scopes, sc)
		}
	}
	if in.ExpiresInDays != nil {
		nt.ExpiresAt = s.now().Add(time.Duration(*in.ExpiresInDays) * 24 * time.Hour)
	}
	secret := tokenPrefix + ids.Token()
	nt.TokenHash = ids.Hash(secret)
	t, err := s.st.CreateAPIToken(ctx, c.actor(), nt)
	if err != nil {
		return err
	}
	s.audit(ctx, c, c.user.ID, store.AuditTokenCreate, "", map[string]string{"token": t.ID})
	v := viewToken(t)
	v.Token = secret
	writeJSON(w, http.StatusCreated, v)
	return nil
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request, c *call) error {
	id := r.PathValue("id")
	if err := s.st.RevokeAPIToken(r.Context(), c.user.ID, id); err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditTokenRevoke, "", map[string]string{"token": id})
	return noContent(w)
}
