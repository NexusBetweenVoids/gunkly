package api

import (
	"context"
	"errors"
	"net/http"
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
	for _, f := range cfgFields {
		if v, ok := s.env[f.env]; ok && f.env != "" {
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
	for k, v := range in.Values {
		f, ok := cfgFieldByKey(k)
		if !ok {
			return errInvalid
		}
		if _, env := s.env[f.env]; env && f.env != "" {
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
