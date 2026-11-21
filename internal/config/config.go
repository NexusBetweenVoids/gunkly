// Package config loads Gunkly's configuration from GUNKLY_* environment
// variables. Parsing is pure: Load takes a lookup function so tests never
// touch the process environment.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// KeySize is the required length of each secret key in bytes (AES-256).
const KeySize = 32

// Config is the complete runtime configuration.
type Config struct {
	DatabaseURL string
	// SecretKeys maps key version to key bytes. CurrentKey is the version used
	// for new ciphertexts; older versions stay readable for rotation.
	SecretKeys       map[int][]byte
	CurrentKey       int
	ListenAddr       string
	BaseURL          string // scheme://host[:port], no trailing slash; may be empty when self-hosting
	TrustedProxies   []netip.Prefix
	EmailEnabled     bool
	SMTP             SMTP
	HostedMode       bool
	AutoMigrate      bool
	DisableScheduler bool
	ShutdownTimeout  time.Duration
	LogLevel         slog.Level
}

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
		ListenAddr:       or(get("LISTEN_ADDR"), ":8080"),
		HostedMode:       boolean("HOSTED_MODE", false),
		EmailEnabled:     boolean("EMAIL_ENABLED", false),
		AutoMigrate:      boolean("AUTO_MIGRATE", true),
		DisableScheduler: boolean("DISABLE_SCHEDULER", false),
		ShutdownTimeout:  30 * time.Second,
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
