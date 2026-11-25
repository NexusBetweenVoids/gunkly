// Package sso is Gunkly's side of OpenID Connect single sign-on (ADR 0018):
// discovery, the authorization URL with PKCE and nonce, the code exchange and
// ID token verification. It knows nothing about accounts; internal/api decides
// who the verified identity is. Self-hosted only.
package sso

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Config is what the administrator set in GUNKLY_OIDC_*.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	Name         string
	Scopes       []string
	RedirectURL  string // {BASE_URL}/api/v1/auth/oidc/callback
}

// Identity is a verified ID token reduced to what Gunkly uses. The identity
// key is (Issuer, Subject); the other fields only suggest a name for a new
// account and are never used to find one.
type Identity struct {
	Issuer, Subject string
	Username        string // preferred_username
	Name            string
	Email           string
	EmailVerified   bool
	AuthTime        time.Time // zero when the provider did not say
}

// ErrUnavailable means the provider's discovery document could not be fetched
// (it may be down or misconfigured); the cause is logged by the caller.
var ErrUnavailable = errors.New("sso: provider unavailable")

// retryAfter spaces out discovery attempts while the provider is unreachable,
// so a down provider does not slow every sign-in page by a timeout.
const retryAfter = 15 * time.Second

// Provider talks to one identity provider. Safe for concurrent use.
type Provider struct {
	cfg    Config
	client *http.Client
	now    func() time.Time

	mu      sync.Mutex
	d       *discovered
	failAt  time.Time
	failErr error
}

type discovered struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// New returns a provider that fetches discovery lazily (the identity provider
// may start after Gunkly) through client, which the caller configured for the
// issuer (egress.NewIdPClient). now may be nil.
func New(cfg Config, client *http.Client, now func() time.Time) *Provider {
	if now == nil {
		now = time.Now
	}
	return &Provider{cfg: cfg, client: client, now: now}
}

// NewVerifier makes a PKCE code verifier.
func NewVerifier() string { return oauth2.GenerateVerifier() }

// Issuer and Name are the configured values.
func (p *Provider) Issuer() string { return p.cfg.Issuer }
func (p *Provider) Name() string   { return p.cfg.Name }

// ClientID, Scopes and RedirectURL describe the setup for the admin page (never the secret).
func (p *Provider) ClientID() string    { return p.cfg.ClientID }
func (p *Provider) Scopes() []string    { return append([]string{}, p.cfg.Scopes...) }
func (p *Provider) RedirectURL() string { return p.cfg.RedirectURL }

// SecretMatches reports whether secret is the configured client secret
// (constant time), so an unchanged configuration can keep its provider.
func (p *Provider) SecretMatches(secret string) bool {
	return subtle.ConstantTimeCompare([]byte(p.cfg.ClientSecret), []byte(secret)) == 1
}

func (p *Provider) discover(ctx context.Context) (*discovered, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.d != nil {
		return p.d, nil
	}
	if p.failErr != nil && p.now().Sub(p.failAt) < retryAfter {
		return nil, p.failErr
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, p.client), 20*time.Second)
	defer cancel()
	prov, err := oidc.NewProvider(ctx, p.cfg.Issuer)
	if err != nil {
		p.failAt, p.failErr = p.now(), fmt.Errorf("%w: %w", ErrUnavailable, err)
		return nil, p.failErr
	}
	var meta struct {
		AuthMethods []string `json:"token_endpoint_auth_methods_supported"`
	}
	_ = prov.Claims(&meta)
	ep := prov.Endpoint()
	// Honour what the provider says it accepts; otherwise let oauth2 find out
	// (Basic first, then the form), which suits providers that do not say.
	switch {
	case p.cfg.ClientSecret == "":
		ep.AuthStyle = oauth2.AuthStyleInParams
	case slices.Contains(meta.AuthMethods, "client_secret_basic") || len(meta.AuthMethods) == 0:
		ep.AuthStyle = oauth2.AuthStyleAutoDetect
	case slices.Contains(meta.AuthMethods, "client_secret_post"):
		ep.AuthStyle = oauth2.AuthStyleInParams
	}
	p.d = &discovered{
		oauth: oauth2.Config{ClientID: p.cfg.ClientID, ClientSecret: p.cfg.ClientSecret, Endpoint: ep,
			RedirectURL: p.cfg.RedirectURL, Scopes: p.cfg.Scopes},
		// The algorithms the provider advertises (go-oidc keeps the safe ones:
		// RS, PS, ES and EdDSA families; never "none" or HMAC) are accepted,
		// which is what Kanidm (ES256) needs.
		verifier: prov.Verifier(&oidc.Config{ClientID: p.cfg.ClientID, Now: p.now}),
	}
	p.failErr = nil
	return p.d, nil
}

// AuthURL is where to send the browser: authorization code flow with PKCE
// (S256), state and nonce. reauth asks the provider to authenticate the user
// again (prompt=login, max_age=0), for step-up.
func (p *Provider) AuthURL(ctx context.Context, state, nonce, verifier string, reauth bool) (string, error) {
	d, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("nonce", nonce)}
	if reauth {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "login"), oauth2.SetAuthURLParam("max_age", "0"))
	}
	return d.oauth.AuthCodeURL(state, opts...), nil
}

// AuthOrigin is the origin (scheme://host) of the provider's authorization
// endpoint, or "" while discovery fails. The consent page's CSP needs it: a
// browser checks form-action against the redirect that answers the form.
func (p *Provider) AuthOrigin(ctx context.Context) string {
	d, err := p.discover(ctx)
	if err != nil {
		return ""
	}
	u, err := url.Parse(d.oauth.Endpoint.AuthURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Exchange redeems the authorization code (with the PKCE verifier) and
// verifies the ID token: signature against the provider's keys, issuer,
// audience (and azp when there are several), expiry and the nonce.
func (p *Provider) Exchange(ctx context.Context, code, verifier, nonce string) (Identity, error) {
	d, err := p.discover(ctx)
	if err != nil {
		return Identity{}, err
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, p.client), 20*time.Second)
	defer cancel()
	tok, err := d.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("sso: code exchange: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return Identity{}, errors.New("sso: no id_token in the token response")
	}
	idt, err := d.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("sso: id token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(nonce)) != 1 || nonce == "" {
		return Identity{}, errors.New("sso: nonce mismatch")
	}
	var c struct {
		Azp               string          `json:"azp"`
		PreferredUsername string          `json:"preferred_username"`
		Name              string          `json:"name"`
		Email             string          `json:"email"`
		EmailVerified     json.RawMessage `json:"email_verified"`
		AuthTime          int64           `json:"auth_time"`
	}
	if err := idt.Claims(&c); err != nil {
		return Identity{}, fmt.Errorf("sso: claims: %w", err)
	}
	if len(idt.Audience) > 1 && c.Azp != p.cfg.ClientID {
		return Identity{}, errors.New("sso: azp does not name this client")
	}
	if idt.Subject == "" || len(idt.Subject) > 255 {
		return Identity{}, errors.New("sso: missing or oversized subject")
	}
	id := Identity{Issuer: idt.Issuer, Subject: idt.Subject, Username: c.PreferredUsername, Name: c.Name, Email: c.Email,
		// Some providers send "true" as a string.
		EmailVerified: strings.Trim(string(c.EmailVerified), `"`) == "true"}
	if c.AuthTime > 0 {
		id.AuthTime = time.Unix(c.AuthTime, 0)
	}
	return id, nil
}
