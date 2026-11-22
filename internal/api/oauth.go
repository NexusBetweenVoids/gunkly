package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/NexusBetweenVoids/gunkly/internal/i18n"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// OAuth 2.1 sign-in for MCP clients (ADR 0016). Optional and off by default:
// every route here answers 404 until an admin turns on oauth_enabled (and only
// while GUNKLY_BASE_URL is https or a loopback address). Access tokens ("gko_")
// authenticate /mcp only.

const (
	oauthAccessPrefix  = "gko_"
	oauthRefreshPrefix = "gkr_"
	oauthSecretLen     = 4 + 43
	maxClientName      = 100
	maxStateLen        = 1024
)

var (
	//go:embed oauth.html
	oauthHTML string
	// oauthTmpl is html/template: every value (the app's self-chosen name
	// above all) is escaped for its context.
	oauthTmpl = template.Must(template.New("oauth").Parse(oauthHTML))

	pkceChallengeRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	pkceVerifierRE  = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

// oauthBaseURLOK reports whether base may be an OAuth issuer: https, or http on
// a loopback host (development).
func oauthBaseURLOK(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.Path != "" || u.User != nil {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && loopbackHost(u.Hostname())
}

// features is the cached pair of instance settings that gate MCP and OAuth.
type features struct{ mcp, oauth bool }

func (s *Server) features(ctx context.Context) features {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if s.mcpAt.IsZero() || time.Since(s.mcpAt) > settingsCacheTTL {
		st, err := s.st.InstanceSettings(ctx)
		if err != nil {
			s.log.Error("feature settings", "err", err)
			return s.feat // the last known values; off until the first read succeeds
		}
		s.feat, s.mcpAt = features{mcp: st.MCPEnabled, oauth: st.OAuthEnabled && st.MCPEnabled && oauthBaseURLOK(s.baseURL)}, time.Now()
	}
	return s.feat
}

func (s *Server) oauthEnabled(ctx context.Context) bool { return s.features(ctx).oauth }

func (s *Server) resourceURL() string { return s.baseURL + "/mcp" }

func (s *Server) metadataURL() string { return s.baseURL + "/.well-known/oauth-protected-resource" }

// oauthScopes are the scope strings in display order.
func oauthScopes() []string { return slices.Clone(tokenScopes) }

// ---- metadata ----

// protectedResource is RFC 9728 metadata for /mcp.
func (s *Server) protectedResource(w http.ResponseWriter, _ *http.Request, _ *call) error {
	return ok(w, map[string]any{
		"resource": s.resourceURL(), "authorization_servers": []string{s.baseURL},
		"scopes_supported": oauthScopes(), "bearer_methods_supported": []string{"header"}, "resource_name": "Gunkly",
	})
}

// authorizationServer is RFC 8414 metadata. Public clients only, S256 only.
func (s *Server) authorizationServer(w http.ResponseWriter, _ *http.Request, _ *call) error {
	return ok(w, map[string]any{
		"issuer": s.baseURL, "authorization_endpoint": s.baseURL + "/oauth/authorize", "token_endpoint": s.baseURL + "/oauth/token",
		"registration_endpoint": s.baseURL + "/oauth/register", "revocation_endpoint": s.baseURL + "/oauth/revoke",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
		"revocation_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": oauthScopes(),
		"authorization_response_iss_parameter_supported": true,
	})
}

// ---- JSON endpoints ----

func oauthError(w http.ResponseWriter, status int, code string) error {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func oauthJSON(w http.ResponseWriter, status int, v any) error {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

// cleanClientName trims and validates a client-chosen display name.
func cleanClientName(n string) (string, bool) {
	n = strings.TrimSpace(n)
	if utf8.RuneCountInString(n) > maxClientName || !utf8.ValidString(n) {
		return "", false
	}
	for _, r := range n {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ' {
			return "", false
		}
	}
	return n, true
}

// registerClient is RFC 7591 dynamic client registration, for public clients
// only: token_endpoint_auth_method is always "none" (a client asking for more
// is told what it got).
func (s *Server) registerClient(w http.ResponseWriter, r *http.Request, _ *call) error {
	var in struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		GrantTypes   []string `json:"grant_types"`
		Response     []string `json:"response_types"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	if err := dec.Decode(&in); err != nil {
		return oauthError(w, http.StatusBadRequest, "invalid_client_metadata")
	}
	name, nameOK := cleanClientName(in.ClientName)
	if !nameOK {
		return oauthError(w, http.StatusBadRequest, "invalid_client_metadata")
	}
	if len(in.GrantTypes) > 0 && !slices.Contains(in.GrantTypes, "authorization_code") ||
		len(in.Response) > 0 && !slices.Contains(in.Response, "code") {
		return oauthError(w, http.StatusBadRequest, "invalid_client_metadata")
	}
	uris := slices.Compact(slices.Sorted(slices.Values(in.RedirectURIs)))
	if len(uris) == 0 || len(uris) > maxRedirectURIs || slices.ContainsFunc(uris, func(u string) bool { return !validRedirectURI(u) }) {
		return oauthError(w, http.StatusBadRequest, "invalid_redirect_uri")
	}
	max, err := s.limits.Limit(r.Context(), limits.OAuthClients, "")
	if err != nil {
		return err
	}
	cl, err := s.st.CreateOAuthClient(r.Context(), name, uris, max)
	if errors.Is(err, store.ErrLimit) {
		return oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
	}
	if err != nil {
		return err
	}
	return oauthJSON(w, http.StatusCreated, map[string]any{
		"client_id": cl.ID, "client_id_issued_at": cl.CreatedAt.Unix(), "client_name": cl.Name, "redirect_uris": cl.RedirectURIs,
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none",
	})
}

// pkceOK checks an S256 code verifier against the stored challenge.
func pkceOK(verifier, challenge string) bool {
	if !pkceVerifierRE.MatchString(verifier) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// oauthToken is the token endpoint: authorization_code (PKCE) and
// refresh_token (rotating, reuse revokes the grant).
func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request, _ *call) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		return oauthError(w, http.StatusBadRequest, "invalid_request")
	}
	f := r.PostForm
	ctx := r.Context()
	clientID := f.Get("client_id")
	if clientID == "" {
		return oauthError(w, http.StatusBadRequest, "invalid_request")
	}
	if res := f.Get("resource"); res != "" && res != s.resourceURL() {
		return oauthError(w, http.StatusBadRequest, "invalid_target")
	}
	cl, err := s.st.OAuthClientByID(ctx, clientID)
	if errors.Is(err, store.ErrNotFound) {
		return oauthError(w, http.StatusUnauthorized, "invalid_client")
	}
	if err != nil {
		return err
	}
	access, refresh := oauthAccessPrefix+ids.Token(), oauthRefreshPrefix+ids.Token()
	var issued store.OAuthIssued
	switch f.Get("grant_type") {
	case "authorization_code":
		code, reused, err := s.st.ConsumeOAuthCode(ctx, ids.Hash(f.Get("code")))
		if reused || errors.Is(err, store.ErrNotFound) {
			return oauthError(w, http.StatusBadRequest, "invalid_grant")
		}
		if err != nil {
			return err
		}
		if code.ClientID != cl.ID || code.RedirectURI != f.Get("redirect_uri") || !pkceOK(f.Get("code_verifier"), code.Challenge) {
			return oauthError(w, http.StatusBadRequest, "invalid_grant")
		}
		if issued, err = s.st.IssueOAuthTokens(ctx, code.GrantID, ids.Hash(access), ids.Hash(refresh)); errors.Is(err, store.ErrNotFound) {
			return oauthError(w, http.StatusBadRequest, "invalid_grant")
		} else if err != nil {
			return err
		}
	case "refresh_token":
		var reused bool
		issued, reused, err = s.st.RotateOAuthRefresh(ctx, ids.Hash(f.Get("refresh_token")), cl.ID, ids.Hash(access), ids.Hash(refresh))
		if reused || errors.Is(err, store.ErrNotFound) {
			return oauthError(w, http.StatusBadRequest, "invalid_grant")
		}
		if err != nil {
			return err
		}
	default:
		return oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
	}
	return oauthJSON(w, http.StatusOK, tokenResponse{AccessToken: access, TokenType: "Bearer", RefreshToken: refresh,
		ExpiresIn: int(issued.AccessExpires.Sub(s.now()).Seconds()), Scope: strings.Join(issued.Scopes, " ")})
}

// oauthRevoke is RFC 7009: revoking either token of a grant ends the grant. It
// answers 200 whatever it found, so it is no oracle.
func (s *Server) oauthRevoke(w http.ResponseWriter, r *http.Request, c *call) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		return oauthError(w, http.StatusBadRequest, "invalid_request")
	}
	tok, clientID := r.PostForm.Get("token"), r.PostForm.Get("client_id")
	if tok == "" || clientID == "" {
		return oauthError(w, http.StatusBadRequest, "invalid_request")
	}
	grant, user, found, err := s.st.RevokeOAuthByToken(r.Context(), ids.Hash(tok), clientID)
	if err != nil {
		return err
	}
	if found {
		s.audit(r.Context(), c, user, store.AuditOAuthRevoke, "", map[string]string{"grant": grant, "via": "revocation_endpoint"})
	}
	return oauthJSON(w, http.StatusOK, map[string]string{})
}

// ---- authorization endpoint (HTML) ----

// authzReq is a validated authorization request.
type authzReq struct {
	client    store.OAuthClient
	redirect  string
	challenge string
	state     string
	resource  string
	scopes    []string // requested, canonical order; every offered scope when the request named none
	explicit  bool     // the request named its scopes
}

// parseAuthz validates the parameters. A bad client_id or redirect_uri may never
// be answered by redirecting (that is the open-redirect hole): bad is true and
// the user sees an error page. Any other fault comes back as an OAuth error
// code to send to the (now trusted) redirect URI.
func (s *Server) parseAuthz(ctx context.Context, v url.Values) (req authzReq, errCode string, bad bool, err error) {
	cl, err := s.st.OAuthClientByID(ctx, v.Get("client_id"))
	if errors.Is(err, store.ErrNotFound) {
		return req, "", true, nil
	}
	if err != nil {
		return req, "", false, err
	}
	ru := v.Get("redirect_uri")
	if !redirectMatches(cl.RedirectURIs, ru) {
		return req, "", true, nil
	}
	req = authzReq{client: cl, redirect: ru, state: v.Get("state"), challenge: v.Get("code_challenge"), resource: v.Get("resource")}
	switch {
	case len(req.state) > maxStateLen:
		return req, "invalid_request", false, nil
	case v.Get("response_type") != "code":
		return req, "unsupported_response_type", false, nil
	case v.Get("code_challenge_method") != "S256" || !pkceChallengeRE.MatchString(req.challenge):
		return req, "invalid_request", false, nil
	case req.resource != "" && req.resource != s.resourceURL():
		return req, "invalid_target", false, nil
	}
	req.scopes = oauthScopes()
	if sc := strings.Fields(v.Get("scope")); len(sc) > 0 {
		for _, x := range sc {
			if !store.ValidScope(x) {
				return req, "invalid_scope", false, nil
			}
		}
		req.explicit = true
		req.scopes = slices.DeleteFunc(oauthScopes(), func(x string) bool { return !slices.Contains(sc, x) })
	}
	return req, "", false, nil
}

// authzRedirect is the 303 back to the client with the given result params.
func (s *Server) authzRedirect(w http.ResponseWriter, r *http.Request, req authzReq, params map[string]string) {
	u, err := url.Parse(req.redirect)
	if err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	if req.state != "" {
		q.Set("state", req.state)
	}
	q.Set("iss", s.baseURL) // RFC 9207: lets the client detect a mix-up
	u.RawQuery = q.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

// oauthNotice shows a plain message page (the done page layout).
func (s *Server) oauthNotice(w http.ResponseWriter, loc string, status int, headKey, noteKey string) error {
	return writeDone(w, status, donePage{Lang: langOf(loc), Title: i18n.T(loc, "oauth.page_title", nil),
		Heading: i18n.T(loc, headKey, nil), Note: i18n.T(loc, noteKey, nil)})
}

type consentHousehold struct {
	ID, Name, Role string
	Checked        bool
}

type consentField struct{ Name, Value string }

type consentPage struct {
	Lang, Title, Heading, UnverifiedLabel, UnverifiedNote, RedirectNote, SignedIn, Error string
	NoHouseholds                                                                         bool
	NoHouseholdsNote                                                                     string
	Hidden                                                                               []consentField
	HouseholdsLegend, ActionsLegend, ActionRead, ActionComplete, ActionWrite             string
	GuestNote, NeverNote, Allow, Deny                                                    string
	Households                                                                           []consentHousehold
	OfferComplete, OfferWrite, CompleteChecked, WriteChecked                             bool
}

// sessionUser authenticates the browser session for the HTML routes, or reports
// that there is none.
func (s *Server) sessionUser(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	_, u, ok := s.authenticate(w, r)
	return u, ok
}

func userLocale(u store.User, r *http.Request) string {
	if u.Locale != "" {
		return u.Locale
	}
	return acceptLocale(r)
}

// authorizeGet is GET /oauth/authorize: sign in if needed, then the consent page.
func (s *Server) authorizeGet(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx := r.Context()
	loc := acceptLocale(r)
	req, code, bad, err := s.parseAuthz(ctx, r.URL.Query())
	if err != nil {
		return err
	}
	if bad {
		return s.oauthNotice(w, loc, http.StatusBadRequest, "oauth.invalid_heading", "oauth.invalid_note")
	}
	if code != "" {
		s.authzRedirect(w, r, req, map[string]string{"error": code})
		return nil
	}
	u, signedIn := s.sessionUser(w, r)
	if !signedIn {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/signin?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return nil
	}
	if err := s.requireAccess(ctx, u); err != nil {
		return s.oauthNotice(w, userLocale(u, r), http.StatusForbidden, "oauth.invalid_heading", "oauth.invalid_note")
	}
	if err := s.st.TouchOAuthClient(ctx, req.client.ID); err != nil {
		return err
	}
	return s.renderConsent(w, r, u, req, nil, "")
}

// renderConsent writes the consent page. picked preselects households (nil:
// all); errKey is an i18n key for a form error.
func (s *Server) renderConsent(w http.ResponseWriter, r *http.Request, u store.User, req authzReq, picked []string, errKey string) error {
	ctx := r.Context()
	loc := userLocale(u, r)
	hs, err := s.st.ListHouseholds(ctx, store.Actor{UserID: u.ID})
	if err != nil {
		return err
	}
	name := req.client.Name
	if name == "" {
		name = i18n.T(loc, "oauth.unnamed_app", nil)
	}
	t := func(k string) string { return i18n.T(loc, k, nil) }
	p := consentPage{Lang: langOf(loc), Title: t("oauth.page_title"),
		Heading:         i18n.T(loc, "oauth.heading", map[string]string{"app": name}),
		UnverifiedLabel: t("oauth.unverified_label"), UnverifiedNote: t("oauth.unverified_note"),
		RedirectNote: i18n.T(loc, "oauth.redirect_note", map[string]string{"host": mustHost(req.redirect)}),
		SignedIn:     i18n.T(loc, "oauth.signed_in_as", map[string]string{"name": u.DisplayName}),
		NoHouseholds: len(hs) == 0, NoHouseholdsNote: t("oauth.no_households"),
		HouseholdsLegend: t("oauth.households"), ActionsLegend: t("oauth.actions"), ActionRead: t("oauth.action_read"),
		ActionComplete: t("oauth.action_complete"), ActionWrite: t("oauth.action_write"),
		GuestNote: t("oauth.guest_note"), NeverNote: t("oauth.never_note"), Allow: t("oauth.allow"), Deny: t("oauth.deny"),
	}
	if errKey != "" {
		p.Error = t(errKey)
	}
	for _, h := range hs {
		role, err := s.st.Role(ctx, store.Actor{UserID: u.ID}, h.ID)
		if err != nil {
			return err
		}
		p.Households = append(p.Households, consentHousehold{ID: h.ID, Name: h.Name, Role: t("oauth.role_" + string(role)),
			Checked: picked == nil || slices.Contains(picked, h.ID)})
	}
	p.OfferComplete, p.OfferWrite = slices.Contains(req.scopes, store.ScopeComplete), slices.Contains(req.scopes, store.ScopeWrite)
	// Least privilege: create is ticked only when the app asked for it by name.
	p.CompleteChecked, p.WriteChecked = p.OfferComplete, p.OfferWrite && req.explicit
	if r.Method == http.MethodPost { // keep the user's choices on a re-render
		p.CompleteChecked, p.WriteChecked = r.PostForm.Get("complete") != "", r.PostForm.Get("write") != ""
	}
	for _, k := range []string{"client_id", "redirect_uri", "response_type", "code_challenge", "code_challenge_method", "state", "scope", "resource"} {
		if v := authzValue(r, k); v != "" {
			p.Hidden = append(p.Hidden, consentField{k, v})
		}
	}
	p.Hidden = append(p.Hidden, consentField{"csrf", s.ensureCSRF(w, r)})
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	// The browser checks form-action against the redirect that answers the
	// post, so the client's own origin (already matched against its registered
	// redirect URIs) is added for this page only.
	h.Set("Content-Security-Policy", strings.Replace(csp, "form-action 'self'", "form-action 'self' "+redirectOrigin(req.redirect), 1))
	w.WriteHeader(http.StatusOK)
	return oauthTmpl.Execute(w, p)
}

func authzValue(r *http.Request, k string) string {
	if r.Method == http.MethodPost {
		return r.PostForm.Get(k)
	}
	return r.URL.Query().Get(k)
}

func mustHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// authorizePost is POST /oauth/authorize: the user's decision. CSRF (cookie
// token echoed as a form field, plus the cross-site check) was applied by the
// router.
func (s *Server) authorizePost(w http.ResponseWriter, r *http.Request, _ *call) error {
	ctx := r.Context()
	loc := acceptLocale(r)
	if err := r.ParseForm(); err != nil {
		return s.oauthNotice(w, loc, http.StatusBadRequest, "oauth.invalid_heading", "oauth.invalid_note")
	}
	u, signedIn := s.sessionUser(w, r)
	if !signedIn {
		return s.oauthNotice(w, loc, http.StatusUnauthorized, "oauth.session_heading", "oauth.session_note")
	}
	loc = userLocale(u, r)
	req, code, bad, err := s.parseAuthz(ctx, r.PostForm)
	if err != nil {
		return err
	}
	if bad || code != "" {
		return s.oauthNotice(w, loc, http.StatusBadRequest, "oauth.invalid_heading", "oauth.invalid_note")
	}
	if r.PostForm.Get("decision") != "allow" {
		s.authzRedirect(w, r, req, map[string]string{"error": "access_denied"})
		return nil
	}
	if err := s.requireAccess(ctx, u); err != nil {
		return s.oauthNotice(w, loc, http.StatusForbidden, "oauth.invalid_heading", "oauth.invalid_note")
	}
	picked := slices.Compact(slices.Sorted(slices.Values(r.PostForm["hh"])))
	if len(picked) == 0 || len(picked) > maxTokenHH {
		return s.renderConsent(w, r, u, req, nil, "oauth.pick_household")
	}
	scopes := []string{store.ScopeRead}
	if r.PostForm.Get("complete") != "" && slices.Contains(req.scopes, store.ScopeComplete) {
		scopes = append(scopes, store.ScopeComplete)
	}
	if r.PostForm.Get("write") != "" && slices.Contains(req.scopes, store.ScopeWrite) {
		scopes = append(scopes, store.ScopeWrite)
	}
	max, err := s.limits.Limit(ctx, limits.OAuthGrantsPerUser, "")
	if err != nil {
		return err
	}
	rawCode := ids.Token()
	_, err = s.st.CreateOAuthCode(ctx, store.Actor{UserID: u.ID}, store.NewOAuthCode{ClientID: req.client.ID, RedirectURI: req.redirect,
		Challenge: req.challenge, CodeHash: ids.Hash(rawCode), Scopes: scopes, HouseholdIDs: picked, Max: max})
	switch {
	case errors.Is(err, store.ErrLimit):
		return s.oauthNotice(w, loc, http.StatusConflict, "oauth.limit_heading", "oauth.limit_note")
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrForbidden):
		return s.oauthNotice(w, loc, http.StatusBadRequest, "oauth.invalid_heading", "oauth.invalid_note")
	case err != nil:
		return err
	}
	s.authzRedirect(w, r, req, map[string]string{"code": rawCode})
	return nil
}

// ---- connected apps (the user's own grants) ----

type grantView struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Scopes     []string             `json:"scopes"`
	Households []tokenHouseholdView `json:"households"`
	CreatedAt  time.Time            `json:"createdAt"`
	LastUsedAt *time.Time           `json:"lastUsedAt"`
}

func (s *Server) listOAuthGrants(w http.ResponseWriter, r *http.Request, c *call) error {
	gs, err := s.st.ListOAuthGrants(r.Context(), c.user.ID)
	if err != nil {
		return err
	}
	out := make([]grantView, 0, len(gs))
	for _, g := range gs {
		out = append(out, grantView{ID: g.ID, Name: g.ClientName, Scopes: g.Scopes, Households: viewTokenHouseholds(g.Households),
			CreatedAt: g.CreatedAt, LastUsedAt: timePtr(g.LastUsedAt)})
	}
	return ok(w, out)
}

func (s *Server) revokeOAuthGrant(w http.ResponseWriter, r *http.Request, c *call) error {
	id := r.PathValue("id")
	if err := s.st.RevokeOAuthGrant(r.Context(), c.user.ID, id); err != nil {
		return err
	}
	s.audit(r.Context(), c, c.user.ID, store.AuditOAuthRevoke, "", map[string]string{"grant": id, "via": "disconnect"})
	return noContent(w)
}
