// Package config loads Gunkly's configuration from GUNKLY_* environment
// variables. Parsing is pure: Load takes a lookup function so tests never
// touch the process environment.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/netip"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// KeySize is the required length of each secret key in bytes (AES-256).
const KeySize = 32

// Config is the complete runtime configuration.
type Config struct {
	DatabaseURL string
	// SecretKeys maps key version to key bytes. CurrentKey is the version used
	// for new ciphertexts; older versions stay readable for rotation.
	SecretKeys     map[int][]byte
	CurrentKey     int
	ListenAddr     string
	BaseURL        string // scheme://host[:port], no trailing slash; may be empty when self-hosting
	TrustedProxies []netip.Prefix
	EmailEnabled   bool
	SMTP           SMTP
	// PoWMaxNumber is the sign-up proof-of-work difficulty: the largest number
	// a client may have to try (about 75k takes a laptop around a second).
	PoWMaxNumber     int
	HostedMode       bool
	AutoMigrate      bool
	DisableScheduler bool
	ShutdownTimeout  time.Duration
	LogLevel         slog.Level
	// MetricsToken enables GET /metrics (Prometheus) behind
	// "Authorization: Bearer <token>"; empty = no metrics endpoint.
	MetricsToken string
	// Overrides holds the set (non-empty) variables of OverrideVars: settings an
	// admin can otherwise change in the app, where these always win.
	Overrides map[string]string
	// OIDC configures single sign-on with an OpenID Connect provider (ADR
	// 0018); the zero value (no Issuer) means off. Self-hosted only.
	OIDC OIDC
}

// OIDC is the single sign-on configuration (GUNKLY_OIDC_*).
type OIDC struct {
	Issuer       string // the provider's issuer URL, exactly as in its discovery document
	ClientID     string
	ClientSecret string   // empty for a public client (PKCE is always used)
	Name         string   // button label ("Authelia"); empty = generic text
	Scopes       []string // always includes "openid"
}

// MinMetricsTokenLen is the shortest accepted GUNKLY_METRICS_TOKEN.
const MinMetricsTokenLen = 16

// SMTP holds outbound mail settings; only used when EmailEnabled.
type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLS      string // "starttls" (default), "tls" or "none"
}

// Load builds a Config from lookup (normally os.Getenv), applying defaults
// and validating everything. Errors never include secret values.
// OverrideVars are the variables of settings that an admin can also change in
// Administration (internal/api's configuration list; a test keeps them in
// step). Load only collects them; internal/api validates and applies them.
var OverrideVars = []string{
	"GUNKLY_AI_ENABLED", "GUNKLY_AI_URL", "GUNKLY_AI_MODEL", "GUNKLY_AI_KEY",
	"GUNKLY_EMAIL_ENABLED", "GUNKLY_SMTP_HOST", "GUNKLY_SMTP_PORT", "GUNKLY_SMTP_USERNAME", "GUNKLY_SMTP_PASSWORD", "GUNKLY_SMTP_FROM", "GUNKLY_SMTP_TLS",
	"GUNKLY_OIDC_ISSUER", "GUNKLY_OIDC_CLIENT_ID", "GUNKLY_OIDC_CLIENT_SECRET", "GUNKLY_OIDC_SCOPES",
	"GUNKLY_METRICS_TOKEN", "GUNKLY_SIGNUP_POW_MAX",
	"GUNKLY_UPDATE_WEBHOOK_URL", "GUNKLY_UPDATE_WEBHOOK_TOKEN", "GUNKLY_UPDATE_WEBHOOK_KIND",
}

func Load(lookup func(string) string) (*Config, error) {
	get := func(k string) string { return strings.TrimSpace(lookup("GUNKLY_" + k)) }
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	boolean := func(k string, def bool) bool {
		v := get(k)
		if v == "" {
			return def
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			fail("GUNKLY_%s: not a boolean", k)
		}
		return b
	}

	c := &Config{
		DatabaseURL:      or(get("DATABASE_URL"), "sqlite:///data/gunkly.db"),
		ListenAddr:       or(get("LISTEN_ADDR"), ":4865"),
		HostedMode:       boolean("HOSTED_MODE", false),
		EmailEnabled:     boolean("EMAIL_ENABLED", false),
		AutoMigrate:      boolean("AUTO_MIGRATE", true),
		DisableScheduler: boolean("DISABLE_SCHEDULER", false),
		ShutdownTimeout:  30 * time.Second,
		PoWMaxNumber:     250_000,
	}

	if !strings.HasPrefix(c.DatabaseURL, "sqlite://") && !strings.HasPrefix(c.DatabaseURL, "postgres://") && !strings.HasPrefix(c.DatabaseURL, "postgresql://") {
		fail("GUNKLY_DATABASE_URL: must start with sqlite:// or postgres://")
	}

	keys, cur, err := parseKeys(get("SECRET_KEYS"), get("SECRET_KEY_CURRENT"))
	if err != nil {
		errs = append(errs, err)
	}
	c.SecretKeys, c.CurrentKey = keys, cur

	if v := get("BASE_URL"); v != "" {
		u, err := url.Parse(v)
		switch {
		case err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https"):
			fail("GUNKLY_BASE_URL: must be an absolute http(s) URL")
		case strings.TrimSuffix(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil:
			fail("GUNKLY_BASE_URL: must be scheme://host[:port] only (sub-path hosting is not supported)")
		default:
			c.BaseURL = u.Scheme + "://" + u.Host
		}
	}

	for _, s := range split(get("TRUSTED_PROXIES")) {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			a, aerr := netip.ParseAddr(s)
			if aerr != nil {
				fail("GUNKLY_TRUSTED_PROXIES: %q is not a CIDR or IP", s)
				continue
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		c.TrustedProxies = append(c.TrustedProxies, p.Masked())
	}

	if v := get("SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			fail("GUNKLY_SHUTDOWN_TIMEOUT: must be a positive duration such as 30s")
		}
		c.ShutdownTimeout = d
	}

	c.OIDC = loadOIDC(get, lookup, c, fail)

	if v := get("METRICS_TOKEN"); v != "" {
		if len(v) < MinMetricsTokenLen {
			fail("GUNKLY_METRICS_TOKEN: must be at least %d characters (generate with: openssl rand -hex 24)", MinMetricsTokenLen)
		}
		c.MetricsToken = v
	}

	c.Overrides = map[string]string{}
	for _, k := range OverrideVars {
		if v := strings.TrimSpace(lookup(k)); v != "" {
			c.Overrides[k] = v
		}
	}

	if v := get("LOG_LEVEL"); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			fail("GUNKLY_LOG_LEVEL: must be debug, info, warn or error")
		}
	}

	c.SMTP = SMTP{
		Host:     get("SMTP_HOST"),
		Port:     587,
		Username: get("SMTP_USERNAME"),
		Password: lookup("GUNKLY_SMTP_PASSWORD"), // not trimmed: may legitimately contain spaces
		From:     get("SMTP_FROM"),
		TLS:      strings.ToLower(or(get("SMTP_TLS"), "starttls")),
	}
	if v := get("SMTP_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			fail("GUNKLY_SMTP_PORT: must be 1-65535")
		}
		c.SMTP.Port = p
	}
	if c.SMTP.TLS != "starttls" && c.SMTP.TLS != "tls" && c.SMTP.TLS != "none" {
		fail("GUNKLY_SMTP_TLS: must be starttls, tls or none")
	}
	if c.EmailEnabled && (c.SMTP.Host == "" || c.SMTP.From == "") {
		fail("GUNKLY_EMAIL_ENABLED requires GUNKLY_SMTP_HOST and GUNKLY_SMTP_FROM")
	}
	if c.EmailEnabled && c.SMTP.From != "" {
		if _, err := mail.ParseAddress(c.SMTP.From); err != nil {
			fail("GUNKLY_SMTP_FROM: not a valid address")
		}
	}
	if c.EmailEnabled && c.BaseURL == "" {
		fail("GUNKLY_EMAIL_ENABLED requires GUNKLY_BASE_URL (emailed links point at it)")
	}
	if v := get("SIGNUP_POW_MAX"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1_000 || n > 1_000_000 {
			fail("GUNKLY_SIGNUP_POW_MAX: must be 1000-1000000")
		}
		c.PoWMaxNumber = n
	}

	// Hosted mode: the public multi-tenant service. Stricter requirements.
	if c.HostedMode {
		if !c.EmailEnabled {
			fail("GUNKLY_HOSTED_MODE requires GUNKLY_EMAIL_ENABLED=true")
		}
		if !strings.HasPrefix(c.BaseURL, "https://") {
			fail("GUNKLY_HOSTED_MODE requires an https GUNKLY_BASE_URL")
		}
		if c.SMTP.TLS == "none" {
			fail("GUNKLY_HOSTED_MODE forbids GUNKLY_SMTP_TLS=none")
		}
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return c, nil
}

// parseKeys parses "1:base64,2:base64". The current version is explicit when
// given, otherwise the highest version.
func parseKeys(list, current string) (map[int][]byte, int, error) {
	if list == "" {
		return nil, 0, errors.New("GUNKLY_SECRET_KEYS: required (generate with: openssl rand -base64 32)")
	}
	keys := map[int][]byte{}
	for _, item := range split(list) {
		vs, b64, ok := strings.Cut(item, ":")
		v, err := strconv.Atoi(vs)
		if !ok || err != nil || v < 1 {
			return nil, 0, errors.New("GUNKLY_SECRET_KEYS: entries must look like <version>:<base64 key>, version >= 1")
		}
		if _, dup := keys[v]; dup {
			return nil, 0, fmt.Errorf("GUNKLY_SECRET_KEYS: version %d given twice", v)
		}
		k, err := decodeKey(b64)
		if err != nil {
			return nil, 0, fmt.Errorf("GUNKLY_SECRET_KEYS: version %d: %w", v, err)
		}
		keys[v] = k
	}
	vers := make([]int, 0, len(keys))
	for v := range keys {
		vers = append(vers, v)
	}
	sort.Ints(vers)
	cur := vers[len(vers)-1]
	if current != "" {
		v, err := strconv.Atoi(current)
		if err != nil || keys[v] == nil {
			return nil, 0, errors.New("GUNKLY_SECRET_KEY_CURRENT: must name a version present in GUNKLY_SECRET_KEYS")
		}
		cur = v
	}
	return keys, cur, nil
}

func decodeKey(s string) ([]byte, error) {
	var k []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if k, err = enc.DecodeString(s); err == nil {
			break
		}
	}
	if err != nil {
		return nil, errors.New("not valid base64")
	}
	if len(k) != KeySize {
		return nil, fmt.Errorf("must decode to exactly %d bytes", KeySize)
	}
	// Reject obviously non-random keys (all zero, all one byte value).
	same := true
	for _, b := range k[1:] {
		if b != k[0] {
			same = false
			break
		}
	}
	if same {
		return nil, errors.New("is not random")
	}
	return k, nil
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// loadOIDC reads GUNKLY_OIDC_*. Any of them set means "SSO wanted", which then
// needs an issuer and a client ID, a base URL the redirect can point at and a
// self-hosted instance.
func loadOIDC(get func(string) string, lookup func(string) string, c *Config, fail func(string, ...any)) OIDC {
	o := OIDC{Issuer: get("OIDC_ISSUER"), ClientID: get("OIDC_CLIENT_ID"),
		ClientSecret: lookup("GUNKLY_OIDC_CLIENT_SECRET"), Name: get("OIDC_NAME")}
	scopes := strings.Fields(or(get("OIDC_SCOPES"), "openid profile email"))
	if o.Issuer == "" && o.ClientID == "" && o.ClientSecret == "" && o.Name == "" && get("OIDC_SCOPES") == "" {
		return OIDC{}
	}
	if o.Issuer == "" || o.ClientID == "" {
		fail("GUNKLY_OIDC_ISSUER and GUNKLY_OIDC_CLIENT_ID are both required for single sign-on")
		return OIDC{}
	}
	if u, err := url.Parse(o.Issuer); err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil ||
		(u.Scheme != "https" && (u.Scheme != "http" || !loopbackHost(u.Hostname()))) {
		fail("GUNKLY_OIDC_ISSUER: must be an https URL (http only for localhost) without query or fragment")
	}
	if c.HostedMode {
		fail("GUNKLY_OIDC_*: single sign-on is not available in hosted mode")
	}
	if u, err := url.Parse(c.BaseURL); c.BaseURL == "" || err != nil || (u.Scheme != "https" && !loopbackHost(u.Hostname())) {
		fail("GUNKLY_OIDC_*: single sign-on needs GUNKLY_BASE_URL to be an https URL (or localhost): the provider redirects back to it")
	}
	if len(o.Name) > 50 || strings.ContainsFunc(o.Name, unicode.IsControl) {
		fail("GUNKLY_OIDC_NAME: at most 50 characters, no control characters")
	}
	if !slices.Contains(scopes, "openid") {
		scopes = append([]string{"openid"}, scopes...)
	}
	o.Scopes = scopes
	return o
}

func loopbackHost(h string) bool { return h == "localhost" || h == "127.0.0.1" || h == "::1" }
