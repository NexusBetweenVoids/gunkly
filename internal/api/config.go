package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Admin configuration: settings an admin can change in Administration instead
// of in the server's environment. An environment variable always wins: its
// field is shown locked ("set by the environment") and cannot be changed here.
// Secret values are stored encrypted (keyring, bound to the key) and are never
// sent back; the API only says whether one is set.

type cfgKind int

const (
	cfgString cfgKind = iota
	cfgBool
	cfgInt
	cfgSecret
)

type cfgField struct {
	key   string
	env   string // environment variable that overrides it
	group string // when one variable of a group is set, the whole group comes from the environment
	kind  cfgKind
	def   string
	check func(string) bool // extra validation of a non-empty value
	min   int               // cfgInt bounds
	max   int
}

var cfgFields = []cfgField{
	{key: "ai.enabled", env: "GUNKLY_AI_ENABLED", kind: cfgBool, def: "false"},
	{key: "ai.url", env: "GUNKLY_AI_URL", kind: cfgString, check: validAIURL},
	{key: "ai.model", env: "GUNKLY_AI_MODEL", kind: cfgString, check: validModelName},
	{key: "ai.key", env: "GUNKLY_AI_KEY", kind: cfgSecret, check: validHeaderValue},

	// Email (SMTP). Set as a group: one GUNKLY_SMTP_* or GUNKLY_EMAIL_ENABLED
	// variable in the environment locks all of them here.
	{key: "email.enabled", env: "GUNKLY_EMAIL_ENABLED", group: "email", kind: cfgBool, def: "false"},
	{key: "email.host", env: "GUNKLY_SMTP_HOST", group: "email", kind: cfgString, check: validSMTPHost},
	{key: "email.port", env: "GUNKLY_SMTP_PORT", group: "email", kind: cfgInt, def: "587", min: 1, max: 65535},
	{key: "email.username", env: "GUNKLY_SMTP_USERNAME", group: "email", kind: cfgString},
	{key: "email.password", env: "GUNKLY_SMTP_PASSWORD", group: "email", kind: cfgSecret},
	{key: "email.from", env: "GUNKLY_SMTP_FROM", group: "email", kind: cfgString, check: validFrom},
	{key: "email.tls", env: "GUNKLY_SMTP_TLS", group: "email", kind: cfgString, def: "starttls", check: validTLSMode},

	// Single sign-on (OpenID Connect), self-hosted only. Configured when the
	// issuer and client ID are set. The button name stays an admin setting.
	{key: "sso.issuer", env: "GUNKLY_OIDC_ISSUER", group: "sso", kind: cfgString, check: validIssuer},
	{key: "sso.client_id", env: "GUNKLY_OIDC_CLIENT_ID", group: "sso", kind: cfgString, check: validModelName},
	{key: "sso.client_secret", env: "GUNKLY_OIDC_CLIENT_SECRET", group: "sso", kind: cfgSecret},
	{key: "sso.scopes", env: "GUNKLY_OIDC_SCOPES", group: "sso", kind: cfgString, def: "openid profile email", check: validScopes},

	{key: "metrics.token", env: "GUNKLY_METRICS_TOKEN", kind: cfgSecret, check: validMetricsToken},
	{key: "signup.pow_max", env: "GUNKLY_SIGNUP_POW_MAX", kind: cfgInt, min: 1_000, max: 1_000_000},

	// Update webhook: an updater the admin runs (Watchtower's HTTP API, a
	// container manager's redeploy webhook, an own script). Gunkly only asks.
	{key: "updates.webhook_url", env: "GUNKLY_UPDATE_WEBHOOK_URL", group: "updatehook", kind: cfgString, check: validUpdateURL},
	{key: "updates.webhook_token", env: "GUNKLY_UPDATE_WEBHOOK_TOKEN", group: "updatehook", kind: cfgSecret, check: validHeaderValue},
}

func cfgFieldByKey(k string) (cfgField, bool) {
	i := slices.IndexFunc(cfgFields, func(f cfgField) bool { return f.key == k })
	if i < 0 {
		return cfgField{}, false
	}
	return cfgFields[i], true
}

// cfgAD binds a secret's ciphertext to its key, so it cannot be moved to another.
func cfgAD(key string) []byte { return []byte("config:" + key) }

// SecretConfigKeys lists the keys gunkly rotate-key must re-encrypt.
func SecretConfigKeys() []string {
	var out []string
	for _, f := range cfgFields {
		if f.kind == cfgSecret {
			out = append(out, f.key)
		}
	}
	return out
}

// ConfigEnvNames lists the environment variables that override admin
// configuration; the caller passes those that are set as Options.Env.
func ConfigEnvNames() []string {
	out := make([]string, 0, len(cfgFields))
	for _, f := range cfgFields {
		out = append(out, f.env)
	}
	return out
}

// ConfigAD is the associated data of a secret configuration value (rotate-key).
func ConfigAD(key string) []byte { return cfgAD(key) }

// envGroups lists the groups the environment configures. Email counts only
// when GUNKLY_EMAIL_ENABLED is true: the SMTP variables never did anything
// without it, and many .env files carry placeholder SMTP values.
func (s *Server) envGroups() map[string]bool {
	out := map[string]bool{"email": strings.EqualFold(s.env["GUNKLY_EMAIL_ENABLED"], "true")}
	for _, f := range cfgFields {
		if _, ok := s.env[f.env]; ok && f.group != "" && f.group != "email" {
			out[f.group] = true
		}
	}
	return out
}

// envValue is the field's environment value, if it counts as set. A variable
// that only repeats the built-in default of the sign-up difficulty (the old
// .env.example set it) does not lock the field.
func (s *Server) envValue(f cfgField) (string, bool) {
	v, ok := s.env[f.env]
	if !ok || f.env == "" || f.group == "email" {
		return "", false
	}
	if f.key == "signup.pow_max" && v == "250000" {
		return "", false
	}
	return v, true
}

// cfgSnapshot is the effective configuration: environment first, then the
// database, then the default.
type cfgSnapshot struct {
	vals    map[string]string // secrets: plaintext, only filled on request
	fromEnv map[string]bool
	stored  map[string]bool
}

func (c cfgSnapshot) str(k string) string { return c.vals[k] }
func (c cfgSnapshot) on(k string) bool    { return c.vals[k] == "true" }

// config loads the effective configuration. Secrets are decrypted only when
// withSecrets is set; a secret that cannot be decrypted (key removed) reads as
// unset and is logged.
func (s *Server) config(ctx context.Context, withSecrets bool) (cfgSnapshot, error) {
	stored, err := s.st.ConfigValues(ctx)
	if err != nil {
		return cfgSnapshot{}, err
	}
	out := cfgSnapshot{vals: map[string]string{}, fromEnv: map[string]bool{}, stored: map[string]bool{}}
	envGroup := s.envGroups()
	for _, f := range cfgFields {
		if v, ok := s.envValue(f); ok || envGroup[f.group] {
			if !ok {
				v = s.env[f.env] // a field of a group the environment sets
			}
			out.fromEnv[f.key] = true
			if f.kind != cfgSecret || withSecrets {
				out.vals[f.key] = v
			} else if v != "" {
				out.stored[f.key] = true
			}
			continue
		}
		v, ok := stored[f.key]
		if !ok {
			out.vals[f.key] = f.def
			continue
		}
		out.stored[f.key] = true
		if f.kind != cfgSecret {
			out.vals[f.key] = v
			continue
		}
		if withSecrets {
			pt, err := s.keyring.Decrypt(v, cfgAD(f.key))
			if err != nil {
				s.log.Error("config secret unreadable (was its secret key removed?)", "key", f.key, "err", err)
				delete(out.stored, f.key)
				continue
			}
			out.vals[f.key] = string(pt)
		}
	}
	return out, nil
}

// configFieldView is one field as the admin screen sees it.
type configFieldView struct {
	Value  *string `json:"value"`  // nil for secrets
	Set    bool    `json:"set"`    // a value is stored or comes from the environment
	Locked bool    `json:"locked"` // set by the environment: read-only here
	Env    string  `json:"env"`    // the variable that would override it
}

func (s *Server) adminGetConfig(w http.ResponseWriter, r *http.Request, _ *call) error {
	cfg, err := s.config(r.Context(), false)
	if err != nil {
		return err
	}
	out := map[string]configFieldView{}
	for _, f := range cfgFields {
		v := configFieldView{Set: cfg.stored[f.key] || cfg.fromEnv[f.key], Locked: cfg.fromEnv[f.key], Env: f.env}
		if f.kind != cfgSecret {
			val := cfg.vals[f.key]
			v.Value = &val
		}
		out[f.key] = v
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": out})
	return nil
}

var errConfigLocked = errf(http.StatusConflict, "set_by_environment")

// adminPatchConfig sets fields: {"values": {"ai.url": "...", "ai.key": null}}.
// null (or "") resets a field to its default; a secret is encrypted.
func (s *Server) adminPatchConfig(w http.ResponseWriter, r *http.Request, c *call) error {
	var in struct {
		Values map[string]*string `json:"values"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if len(in.Values) == 0 || len(in.Values) > len(cfgFields) {
		return errInvalid
	}
	write := map[string]*string{}
	var keys []string
	cur, err := s.config(r.Context(), false)
	if err != nil {
		return err
	}
	for k, v := range in.Values {
		f, ok := cfgFieldByKey(k)
		if !ok {
			return errInvalid
		}
		if cur.fromEnv[k] {
			return errConfigLocked
		}
		keys = append(keys, k)
		if v == nil || strings.TrimSpace(*v) == "" {
			write[k] = nil
			continue
		}
		val := strings.TrimSpace(*v)
		if !validConfigValue(f, val) {
			return errInvalid
		}
		if f.kind == cfgSecret {
			val = s.keyring.Encrypt([]byte(val), cfgAD(k))
		}
		write[k] = &val
	}
	if err := s.st.SetConfig(r.Context(), write); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			return errInvalid
		}
		return err
	}
	slices.Sort(keys)
	s.audit(r.Context(), c, c.user.ID, store.AuditAdminSettings, "", map[string]string{"config": strings.Join(keys, ",")})
	if err := s.applyConfig(r.Context()); err != nil {
		return err
	}
	return s.adminGetConfig(w, r, c)
}

func validConfigValue(f cfgField, v string) bool {
	if len(v) > 2048 || strings.ContainsFunc(v, unicode.IsControl) {
		return false
	}
	switch f.kind {
	case cfgBool:
		if v != "true" && v != "false" {
			return false
		}
	case cfgInt:
		n, err := strconv.Atoi(v)
		if err != nil || n < f.min || n > f.max {
			return false
		}
	}
	return f.check == nil || f.check(v)
}

// validAIURL accepts the base URL of an OpenAI-compatible API ("…/v1"):
// http or https, a host, no credentials, query or fragment. Private and local
// addresses are fine (a model on the admin's own network).
func validAIURL(v string) bool {
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil &&
		u.RawQuery == "" && u.Fragment == "" && !strings.Contains(u.Host, " ")
}

func validModelName(v string) bool {
	return len(v) <= 200 && !strings.ContainsAny(v, " \t")
}

// validHeaderValue: an API key goes into an Authorization header.
func validHeaderValue(v string) bool {
	return len(v) <= 1024 && !strings.ContainsAny(v, " \t\r\n")
}

// validSMTPHost: a host name or IP literal, no scheme, port or spaces.
func validSMTPHost(v string) bool {
	return len(v) <= 253 && !strings.ContainsAny(v, " /:@?#") || strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]")
}

// validFrom: "Name <addr@host>" or a bare address.
func validFrom(v string) bool {
	_, err := mail.ParseAddress(v)
	return err == nil && len(v) <= 320
}

func validTLSMode(v string) bool { return v == "starttls" || v == "tls" || v == "none" }

// validIssuer matches the environment rule: https, or http only for localhost.
func validIssuer(v string) bool {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return false
	}
	h := u.Hostname()
	return u.Scheme == "https" || (u.Scheme == "http" && (h == "localhost" || h == "127.0.0.1" || h == "::1"))
}

func validScopes(v string) bool {
	return len(v) <= 500 && !strings.ContainsAny(v, "\"<>")
}

func validMetricsToken(v string) bool {
	return len(v) >= 16 && len(v) <= 256 && !strings.ContainsAny(v, " \t")
}

// validUpdateURL: an http(s) URL with a host and no credentials; a query is
// allowed (some updaters put their token there).
func validUpdateURL(v string) bool {
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Fragment == "" && len(v) <= 2000
}
