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
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"

	"github.com/NexusBetweenVoids/gunkly/internal/i18n"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"golang.org/x/text/unicode/norm"
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
type features struct{ mcp, oauth, webhooks bool }

func (s *Server) features(ctx context.Context) features {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if s.mcpAt.IsZero() || time.Since(s.mcpAt) > settingsCacheTTL {
		st, err := s.st.InstanceSettings(ctx)
		if err != nil {
			s.log.Error("feature settings", "err", err)
			return s.feat // the last known values; off until the first read succeeds
		}
		s.feat, s.mcpAt = features{mcp: st.MCPEnabled, oauth: st.OAuthEnabled && st.MCPEnabled && oauthBaseURLOK(s.baseURL), webhooks: st.WebhooksEnabled}, time.Now()
	}
	return s.feat
}

func (s *Server) oauthEnabled(ctx context.Context) bool { return s.features(ctx).oauth }

// webhooksEnabled reports the webhooks_enabled instance setting (default off),
// cached briefly like the others.
func (s *Server) webhooksEnabled(ctx context.Context) bool { return s.features(ctx).webhooks }

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
	if impersonates(n) {
		return "", false
	}
	return n, true
}

// impersonates reports whether a client name contains "gunkly" once folded
// (compatibility decomposition, combining marks dropped, lower case), so
// fullwidth, styled and accented lookalikes do not pass for the product.
func impersonates(n string) bool {
	var b strings.Builder
	for _, r := range norm.NFKD.String(n) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return strings.Contains(b.String(), "gunkly")
}

// registerClient is RFC 7591 dynamic client registration, for public clients
// only: token_endpoint_auth_method is always "none" (a client asking for more
// is told what it got).
func (s *Server) registerClient(w http.ResponseWriter, r *http.Request, c *call) error {
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
	perIP, err := s.limits.Limit(r.Context(), limits.OAuthUnusedClientsPerIP, "")
	if err != nil {
		return err
	}
	cl, err := s.st.CreateOAuthClient(r.Context(), name, uris, max, perIP, c.rip)
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
	Lang, Title, Heading, UnverifiedLabel, UnverifiedNote, ReturnLabel, ReturnHost, NewHostWarning, SignedIn, Error, Info string
	NoHouseholds                                                                                                          bool
	NoHouseholdsNote                                                                                                      string
	Hidden                                                                                                                []consentField
	HouseholdsLegend, ActionsLegend, ActionRead, ActionComplete, ActionWrite                                              string
	GuestNote, NeverNote, Allow, Deny                                                                                     string
	Households                                                                                                            []consentHousehold
	OfferComplete, OfferWrite, CompleteChecked, WriteChecked                                                              bool
	// Step-up: the password (and second factor) fields, shown when this
	// session has not re-verified within stepUpWindow.
	StepUp, ShowPassword, ShowCode, OfferEmail                         bool
	ConfirmLegend, ConfirmNote, PasswordLabel, CodeLabel, EmailCodeBtn string
	// Passkey is offered (by the script, ADR 0016) when the account has one; SSO
	// when the account is linked to the instance's identity provider.
	Passkey, SSO               bool
	PasskeyLabel, PasskeyError string
	SSOLabel                   string
}

// sessionUser authenticates the browser session for the HTML routes, or reports
// that there is none.
func (s *Server) sessionUser(w http.ResponseWriter, r *http.Request) (store.Session, store.User, bool) {
	return s.authenticate(w, r)
}

func userLocale(u store.User, r *http.Request) string {
	if u.Locale != "" {
		return u.Locale
	}
	return acceptLocale(r)
}

// steppedUp reports whether the session re-verified within stepUpWindow.
func (s *Server) steppedUp(ss store.Session) bool {
	return !ss.StepUpAt.IsZero() && s.now().Sub(ss.StepUpAt) <= stepUpWindow
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
	ss, u, signedIn := s.sessionUser(w, r)
	if !signedIn {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/signin?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return nil
	}
	if err := s.requireAccess(ctx, u); err != nil {
		return s.oauthNotice(w, userLocale(u, r), http.StatusForbidden, "oauth.invalid_heading", "oauth.invalid_note")
	}
	cc := consentCtx{u: u, ss: ss, req: req}
	// The way back from the identity provider says only how it went. It is a
	// message, never trusted: the step-up itself is the session's stepped-up
	// state, set by the callback after it checked the provider's answer.
	switch r.URL.Query().Get("sso") {
	case "":
	case "stepup":
		if s.steppedUp(ss) {
			cc.infoKey = "oauth.sso_confirmed"
		}
	default:
		cc.errKey = "oauth.sso_failed"
	}
	return s.renderConsent(w, r, cc)
}

// consentCtx is what renderConsent needs beyond the request.
type consentCtx struct {
	u        store.User
	ss       store.Session
	req      authzReq
	picked   []string // households to preselect (nil: all)
	errKey   string   // i18n key of a form error
	infoKey  string   // i18n key of a notice (the emailed code was sent)
	showCode bool     // the code field is shown even for users without an authenticator app
}

// renderConsent writes the consent page.
func (s *Server) renderConsent(w http.ResponseWriter, r *http.Request, cc consentCtx) error {
	ctx := r.Context()
	u, req := cc.u, cc.req
	loc := userLocale(u, r)
	hs, err := s.st.ListHouseholds(ctx, store.Actor{UserID: u.ID})
	if err != nil {
		return err
	}
	seen, err := s.st.OAuthRedirectsApproved(ctx, u.ID)
	if err != nil {
		return err
	}
	host := hostnameOf(req.redirect)
	name := req.client.Name
	if name == "" {
		name = i18n.T(loc, "oauth.unnamed_app", nil)
	}
	t := func(k string) string { return i18n.T(loc, k, nil) }
	p := consentPage{Lang: langOf(loc), Title: t("oauth.page_title"),
		Heading:         i18n.T(loc, "oauth.heading", map[string]string{"app": name}),
		UnverifiedLabel: t("oauth.unverified_label"), UnverifiedNote: t("oauth.unverified_note"),
		ReturnLabel: t("oauth.return_label"), ReturnHost: mustHost(req.redirect),
		SignedIn:     i18n.T(loc, "oauth.signed_in_as", map[string]string{"name": u.DisplayName}),
		NoHouseholds: len(hs) == 0, NoHouseholdsNote: t("oauth.no_households"),
		HouseholdsLegend: t("oauth.households"), ActionsLegend: t("oauth.actions"), ActionRead: t("oauth.action_read"),
		ActionComplete: t("oauth.action_complete"), ActionWrite: t("oauth.action_write"),
		GuestNote: t("oauth.guest_note"), NeverNote: t("oauth.never_note"), Allow: t("oauth.allow"), Deny: t("oauth.deny"),
	}
	if !slices.ContainsFunc(seen, func(x string) bool { return hostnameOf(x) == host }) {
		p.NewHostWarning = i18n.T(loc, "oauth.new_host", map[string]string{"host": mustHost(req.redirect)})
	}
	if cc.errKey != "" {
		p.Error = t(cc.errKey)
	}
	if cc.infoKey != "" {
		p.Info = t(cc.infoKey)
	}
	var ssoOrigin string
	if !s.steppedUp(cc.ss) {
		p.StepUp, p.ShowPassword = true, u.HasPassword()
		p.OfferEmail = u.EmailOTPEnabled && p.ShowPassword
		// Without script a passkey is no use here: such an account types a recovery
		// code next to its password. With script the button below does the ceremony.
		p.ShowCode = p.ShowPassword && (u.TOTPEnabled || cc.showCode || (u.Passkeys > 0 && !u.EmailOTPEnabled))
		p.ConfirmLegend, p.ConfirmNote, p.PasswordLabel, p.CodeLabel, p.EmailCodeBtn =
			t("oauth.confirm_legend"), t("oauth.confirm_note"), t("oauth.password"), t("oauth.code"), t("oauth.email_code")
		if !p.ShowPassword {
			p.ConfirmNote = t("oauth.confirm_note_other")
		}
		if u.Passkeys > 0 && s.passkeyRP() != nil {
			p.Passkey, p.PasskeyLabel, p.PasskeyError = true, t("oauth.passkey_btn"), t("oauth.passkey_failed")
		}
		if s.ssoLinked(ctx, u) {
			// The provider's origin joins form-action below: the post is answered by a redirect to it.
			if ssoOrigin = s.sso.AuthOrigin(ctx); ssoOrigin != "" {
				p.SSO, p.SSOLabel = true, i18n.T(loc, "oauth.sso_btn", map[string]string{"provider": s.sso.Name()})
			}
		}
	}
	for _, h := range hs {
		role, err := s.st.Role(ctx, store.Actor{UserID: u.ID}, h.ID)
		if err != nil {
			return err
		}
		p.Households = append(p.Households, consentHousehold{ID: h.ID, Name: h.Name, Role: t("oauth.role_" + string(role)),
			Checked: cc.picked == nil || slices.Contains(cc.picked, h.ID)})
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
	formAction := "form-action 'self' " + redirectOrigin(req.redirect)
	if p.SSO {
		formAction += " " + ssoOrigin
	}
	h.Set("Content-Security-Policy", strings.Replace(csp, "form-action 'self'", formAction, 1))
	w.WriteHeader(http.StatusOK)
	return oauthTmpl.Execute(w, p)
}

func authzValue(r *http.Request, k string) string {
	if r.Method == http.MethodPost {
		return r.PostForm.Get(k)
	}
	return r.URL.Query().Get(k)
}

// hostnameOf is the lower-case host of raw without its port ("" if invalid).
func hostnameOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
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
// router. Deny never needs a password; allow needs one (and a second factor
// when the account has one) unless the session re-verified recently.
func (s *Server) authorizePost(w http.ResponseWriter, r *http.Request, c *call) error {
	ctx := r.Context()
	loc := acceptLocale(r)
	if err := r.ParseForm(); err != nil {
		return s.oauthNotice(w, loc, http.StatusBadRequest, "oauth.invalid_heading", "oauth.invalid_note")
	}
	ss, u, signedIn := s.sessionUser(w, r)
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
	decision := r.PostForm.Get("decision")
	if decision != "allow" && decision != "email_code" && decision != "sso" {
		s.authzRedirect(w, r, req, map[string]string{"error": "access_denied"})
		return nil
	}
	if err := s.requireAccess(ctx, u); err != nil {
		return s.oauthNotice(w, loc, http.StatusForbidden, "oauth.invalid_heading", "oauth.invalid_note")
	}
	picked := slices.Compact(slices.Sorted(slices.Values(r.PostForm["hh"])))
	cc := consentCtx{u: u, ss: ss, req: req, picked: picked, showCode: r.PostForm.Get("code") != ""}
	if decision == "sso" {
		return s.consentSSO(w, r, c, cc)
	}
	if len(picked) == 0 || len(picked) > maxTokenHH {
		cc.picked, cc.errKey = nil, "oauth.pick_household"
		return s.renderConsent(w, r, cc)
	}
	if !s.steppedUp(ss) {
		var out stepUpOutcome
		var err error
		viaPasskey := r.PostForm.Get("passkey") != ""
		if viaPasskey {
			out, err = s.consentPasskey(w, r, c, ss, u)
		} else {
			out, err = s.consentStepUp(w, r, c, ss, u, decision == "email_code")
		}
		if err != nil || out == stepUpThrottled {
			return err
		}
		switch out {
		case stepUpFailed:
			cc.errKey = "oauth.stepup_failed"
			if viaPasskey {
				cc.errKey = "oauth.passkey_failed"
			}
		case stepUpCodeSent:
			cc.infoKey, cc.showCode = "oauth.code_sent", true
		}
		if out != stepUpOK {
			return s.renderConsent(w, r, cc)
		}
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

type stepUpOutcome int

const (
	stepUpOK        stepUpOutcome = iota // verified; the session is stepped up
	stepUpFailed                         // wrong credentials: one generic error whatever was wrong
	stepUpCodeSent                       // the emailed code went out
	stepUpThrottled                      // the 429 page is already written
)

// consentStepUp re-verifies the signed-in user on the consent page with the
// same verifier, per-account backoff and rate limit as POST
// /api/v1/session/step-up, and on success marks the session stepped up. With
// emailRequest it sends the email code instead of checking one.
func (s *Server) consentStepUp(w http.ResponseWriter, r *http.Request, c *call, ss store.Session, u store.User, emailRequest bool) (stepUpOutcome, error) {
	ctx := r.Context()
	loc := userLocale(u, r)
	pwKey, tfKey := "u:"+u.ID, "2fa:"+u.ID
	wait := s.rl.allow(rateTwoFA + ":acct:" + c.rip + "|" + u.ID)
	wait = max(wait, s.acct.wait(pwKey))
	if wait > 0 {
		return stepUpThrottled, s.slowDown(w, loc, wait)
	}
	fail := func(key, why string) (stepUpOutcome, error) {
		s.acct.fail(key)
		s.audit(ctx, c, u.ID, store.AuditLoginFailure, "", map[string]string{"via": "oauth_consent", "reason": why})
		return stepUpFailed, nil
	}
	if good, err := s.verifyPassword(u.PasswordHash, r.PostForm.Get("password")); err != nil || !good {
		return fail(pwKey, "password")
	}
	if u.HasSecondFactor() {
		if emailRequest {
			if !u.EmailOTPEnabled {
				return stepUpFailed, nil
			}
			wait, err := s.sendLoginCode(ctx, c, u, false)
			if err != nil {
				return stepUpFailed, err
			}
			if wait > 0 {
				return stepUpThrottled, s.slowDown(w, loc, wait)
			}
			return stepUpCodeSent, nil
		}
		code := strings.TrimSpace(r.PostForm.Get("code"))
		if code == "" {
			return stepUpFailed, nil
		}
		if wait := s.acct.wait(tfKey); wait > 0 {
			return stepUpThrottled, s.slowDown(w, loc, wait)
		}
		if err := s.consentSecondFactor(ctx, c, u, code); errors.Is(err, errInvalidCode) {
			return fail(tfKey, "second_factor")
		} else if err != nil {
			return stepUpFailed, err
		}
		s.acct.reset(tfKey)
	}
	s.acct.reset(pwKey)
	return stepUpOK, s.st.StepUpSession(ctx, u.ID, ss.ID)
}

// consentPurpose is the passkey ceremony purpose of one session's consent
// page: the purpose and the hash of the session are in the challenge's
// binding, so it works for no other purpose, account or session.
func consentPurpose(ss store.Session) string { return store.PasskeyConsent + ":" + ids.Hash(ss.ID) }

// consentPasskey re-verifies the user with a passkey assertion posted with the
// consent form. The assertion is verified here, by the same code as the
// step-up (user verification required, signature counter, per-account backoff),
// against a challenge this session asked for on this page; the form carries
// nothing a script could set to "confirmed".
func (s *Server) consentPasskey(w http.ResponseWriter, r *http.Request, c *call, ss store.Session, u store.User) (stepUpOutcome, error) {
	ctx := r.Context()
	tfKey := "2fa:" + u.ID
	wait := max(s.rl.allow(rateTwoFA+":acct:"+c.rip+"|"+u.ID), s.acct.wait(tfKey))
	if wait > 0 {
		return stepUpThrottled, s.slowDown(w, userLocale(u, r), wait)
	}
	fail := func() (stepUpOutcome, error) {
		s.acct.fail(tfKey)
		s.audit(ctx, c, u.ID, store.AuditLoginFailure, "", map[string]string{"via": "oauth_consent", "reason": "passkey"})
		return stepUpFailed, nil
	}
	raw := r.PostForm.Get("passkey")
	var ans passkeyAnswer
	if len(raw) > 32<<10 || json.Unmarshal([]byte(raw), &ans) != nil {
		return fail()
	}
	if err := s.verifyPasskey(ctx, c, u, &ans, consentPurpose(ss)); errors.Is(err, errInvalidPasskey) {
		return fail()
	} else if err != nil {
		return stepUpFailed, err
	}
	s.acct.reset(tfKey)
	return stepUpOK, s.st.StepUpSession(ctx, u.ID, ss.ID)
}

// consentPasskeyOptions is the script's first call: the options of an
// assertion for the signed-in user's own passkeys (user verification
// required), saved for five minutes for this session's consent page only.
// CSRF is the consent form's (cookie against the csrf field).
func (s *Server) consentPasskeyOptions(w http.ResponseWriter, r *http.Request, _ *call) error {
	ss, u, signedIn := s.sessionUser(w, r)
	if !signedIn {
		return errUnauthenticated
	}
	if err := s.requireAccess(r.Context(), u); err != nil {
		return errNotFound
	}
	return s.beginAssertion(w, r, u, consentPurpose(ss), protocol.VerificationRequired)
}

//go:embed oauth-consent.js
var oauthConsentJS []byte

// consentScript serves the consent page's one script (same origin, so the
// page's script-src 'self' allows it without inline code).
func (s *Server) consentScript(w http.ResponseWriter, _ *http.Request, _ *call) error {
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	_, err := w.Write(oauthConsentJS)
	return err
}

// ssoLinked reports whether u can confirm through the instance's identity provider.
func (s *Server) ssoLinked(ctx context.Context, u store.User) bool {
	if s.sso == nil {
		return false
	}
	_, err := s.st.SSOIdentityForUser(ctx, u.ID, s.sso.Issuer())
	return err == nil
}

// consentNext is the pending authorization request as a path to come back to
// after the provider: rebuilt from the validated request (client, redirect
// URI, challenge, scopes), never copied from what the form posted.
func consentNext(req authzReq) string {
	q := url.Values{"response_type": {"code"}, "client_id": {req.client.ID}, "redirect_uri": {req.redirect},
		"code_challenge": {req.challenge}, "code_challenge_method": {"S256"}}
	if req.state != "" {
		q.Set("state", req.state)
	}
	if req.explicit {
		q.Set("scope", strings.Join(req.scopes, " "))
	}
	if req.resource != "" {
		q.Set("resource", req.resource)
	}
	return consentNextPrefix + q.Encode()
}

// consentSSO sends the user to the identity provider to confirm, as a step-up
// (prompt=login, max_age=0), and brings them back to this same consent
// request. The way back is only a GET of the consent page, which validates the
// request again from scratch.
func (s *Server) consentSSO(w http.ResponseWriter, r *http.Request, c *call, cc consentCtx) error {
	ctx := r.Context()
	if wait := s.rl.allow(rateSSO + ":ip:" + c.rip); wait > 0 {
		return s.slowDown(w, userLocale(cc.u, r), wait)
	}
	next := consentNext(cc.req)
	cc.picked, cc.errKey = nil, "oauth.sso_failed"
	if !s.ssoLinked(ctx, cc.u) || len(next) > ssoMaxConsentNext {
		return s.renderConsent(w, r, cc)
	}
	to, err := s.ssoBegin(ctx, w, ssoModeStepUp, cc.u.ID, cc.ss.ID, next)
	if err != nil {
		s.log.Warn("sso consent", "err", err)
		return s.renderConsent(w, r, cc)
	}
	redirect(w, r, to)
	return nil
}

// consentSecondFactor checks one code field: six digits are an authenticator
// or emailed code, anything else a recovery code.
func (s *Server) consentSecondFactor(ctx context.Context, c *call, u store.User, code string) error {
	digits := strings.ReplaceAll(code, " ", "")
	if len(digits) != 6 || strings.Trim(digits, "0123456789") != "" {
		return s.secondFactor(ctx, c, u, credentials{RecoveryCode: code})
	}
	err := error(errInvalidCode)
	if u.TOTPEnabled {
		err = s.secondFactor(ctx, c, u, credentials{TOTP: digits})
	}
	if errors.Is(err, errInvalidCode) && u.EmailOTPEnabled {
		err = s.secondFactor(ctx, c, u, credentials{EmailCode: digits})
	}
	return err
}

// slowDown answers a throttled consent step-up with a 429 page.
func (s *Server) slowDown(w http.ResponseWriter, loc string, wait time.Duration) error {
	w.Header().Set("Retry-After", strconv.Itoa(max(int((wait+time.Second-1)/time.Second), 1)))
	return writeDone(w, http.StatusTooManyRequests, donePage{Lang: langOf(loc), Title: i18n.T(loc, "oauth.page_title", nil),
		Heading: i18n.T(loc, "oauth.slow_heading", nil), Note: i18n.T(loc, "oauth.slow_note", nil)})
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

// tokenSource is the completion source for a bearer credential: an OAuth
// grant or an API token.
func tokenSource(k *store.APIToken) string {
	if k.OAuth {
		return store.SourceOAuth
	}
	return store.SourceAPIToken
}
