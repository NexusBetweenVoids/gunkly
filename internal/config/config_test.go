package config

import (
	"encoding/base64"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func key(b byte) string {
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = b + byte(i)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func base() map[string]string {
	return map[string]string{"GUNKLY_SECRET_KEYS": "1:" + key(1)}
}

func TestDefaults(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL != "sqlite:///data/gunkly.db" || c.ListenAddr != ":4865" || !c.AutoMigrate ||
		c.EmailEnabled || c.HostedMode || c.DisableScheduler || c.ShutdownTimeout != 30*time.Second ||
		c.LogLevel != slog.LevelInfo || c.CurrentKey != 1 || len(c.SecretKeys[1]) != KeySize || c.SMTP.Port != 587 || c.SMTP.TLS != "starttls" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestFull(t *testing.T) {
	m := base()
	for k, v := range map[string]string{
		"DATABASE_URL":      "postgres://u:p@db/gunkly",
		"SECRET_KEYS":       "1:" + key(1) + ", 3:" + key(3) + ",2:" + key(2),
		"LISTEN_ADDR":       "127.0.0.1:9000",
		"BASE_URL":          "https://chores.example.com/",
		"TRUSTED_PROXIES":   "10.0.0.0/8, 192.168.1.7, fd00::/8",
		"EMAIL_ENABLED":     "true",
		"SMTP_HOST":         "smtp.example.com",
		"SMTP_PORT":         "465",
		"SMTP_FROM":         "Gunkly <noreply@example.com>",
		"SMTP_TLS":          "TLS",
		"HOSTED_MODE":       "1",
		"AUTO_MIGRATE":      "false",
		"DISABLE_SCHEDULER": "true",
		"SHUTDOWN_TIMEOUT":  "5s",
		"LOG_LEVEL":         "debug",
	} {
		m["GUNKLY_"+k] = v
	}
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.CurrentKey != 3 || len(c.SecretKeys) != 3 {
		t.Errorf("keys: current %d, n %d", c.CurrentKey, len(c.SecretKeys))
	}
	if c.BaseURL != "https://chores.example.com" {
		t.Errorf("base url %q", c.BaseURL)
	}
	if len(c.TrustedProxies) != 3 || c.TrustedProxies[1].String() != "192.168.1.7/32" {
		t.Errorf("proxies %v", c.TrustedProxies)
	}
	if !c.HostedMode || c.AutoMigrate || !c.DisableScheduler || c.ShutdownTimeout != 5*time.Second || c.LogLevel != slog.LevelDebug || c.SMTP.Port != 465 || c.SMTP.TLS != "tls" {
		t.Errorf("unexpected: %+v", c)
	}

	m["GUNKLY_SECRET_KEY_CURRENT"] = "2"
	if c, err = Load(env(m)); err != nil || c.CurrentKey != 2 {
		t.Errorf("explicit current: %v %v", c, err)
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"no key":           {},
		"empty key":        {"GUNKLY_SECRET_KEYS": "1:"},
		"short key":        {"GUNKLY_SECRET_KEYS": "1:" + base64.StdEncoding.EncodeToString([]byte("short"))},
		"zero key":         {"GUNKLY_SECRET_KEYS": "1:" + base64.StdEncoding.EncodeToString(make([]byte, KeySize))},
		"placeholder key":  {"GUNKLY_SECRET_KEYS": "1:REPLACE_WITH_OUTPUT_OF_openssl_rand_base64_32"},
		"no version":       {"GUNKLY_SECRET_KEYS": key(1)},
		"version zero":     {"GUNKLY_SECRET_KEYS": "0:" + key(1)},
		"dup version":      {"GUNKLY_SECRET_KEYS": "1:" + key(1) + ",1:" + key(2)},
		"unknown current":  {"GUNKLY_SECRET_KEY_CURRENT": "2"},
		"bad db":           {"GUNKLY_DATABASE_URL": "mysql://x"},
		"bad bool":         {"GUNKLY_AUTO_MIGRATE": "maybe"},
		"bad base":         {"GUNKLY_BASE_URL": "ftp://x"},
		"query base":       {"GUNKLY_BASE_URL": "https://x.example/gunkly?a=1"},
		"dotdot base":      {"GUNKLY_BASE_URL": "https://x.example/a/../b"},
		"double slash":     {"GUNKLY_BASE_URL": "https://x.example/a//b"},
		"escaped base":     {"GUNKLY_BASE_URL": "https://x.example/a%2Fb"},
		"bad proxy":        {"GUNKLY_TRUSTED_PROXIES": "10.0.0.0/33"},
		"bad timeout":      {"GUNKLY_SHUTDOWN_TIMEOUT": "-1s"},
		"bad level":        {"GUNKLY_LOG_LEVEL": "loud"},
		"bad port":         {"GUNKLY_SMTP_PORT": "70000"},
		"bad tls":          {"GUNKLY_SMTP_TLS": "ssl3"},
		"email no smtp":    {"GUNKLY_EMAIL_ENABLED": "true"},
		"email no base":    {"GUNKLY_EMAIL_ENABLED": "true", "GUNKLY_SMTP_HOST": "h", "GUNKLY_SMTP_FROM": "a@example.com"},
		"bad from":         {"GUNKLY_EMAIL_ENABLED": "true", "GUNKLY_SMTP_HOST": "h", "GUNKLY_SMTP_FROM": "not an address", "GUNKLY_BASE_URL": "https://x.example"},
		"bad pow":          {"GUNKLY_SIGNUP_POW_MAX": "5"},
		"hosted no email":  {"GUNKLY_HOSTED_MODE": "true", "GUNKLY_BASE_URL": "https://x.example"},
		"legal self-host":  {"GUNKLY_PRIVACY_URL": "https://x.example/privacy"},
		"terms self-host":  {"GUNKLY_TERMS_URL": "https://x.example/terms"},
		"http privacy":     {"GUNKLY_PRIVACY_URL": "http://x.example/privacy"},
		"bad sec contact":  {"GUNKLY_SECURITY_CONTACT": "security@x.example"},
		"sec contact crlf": {"GUNKLY_SECURITY_CONTACT": "https://x.example/\r\nPolicy: x"},
		"policy alone":     {"GUNKLY_SECURITY_POLICY": "https://x.example/policy"},
		"hosted http base": {"GUNKLY_HOSTED_MODE": "true", "GUNKLY_EMAIL_ENABLED": "true", "GUNKLY_SMTP_HOST": "h", "GUNKLY_SMTP_FROM": "f", "GUNKLY_BASE_URL": "http://x.example"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			m := base()
			for k, v := range extra {
				m[k] = v
			}
			if _, ok := extra["GUNKLY_SECRET_KEYS"]; !ok && name == "no key" {
				delete(m, "GUNKLY_SECRET_KEYS")
			}
			if _, err := Load(env(m)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestErrorsDoNotLeakSecrets(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("hunter2-hunter2"))
	_, err := Load(env(map[string]string{"GUNKLY_SECRET_KEYS": "1:" + secret}))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v", err)
	}
}

// TestEnvExampleListsEveryKey keeps .env.example in step with Load: every
// GUNKLY_* variable Load reads must be documented there, and it must not
// document variables Load ignores.
func TestEnvExampleListsEveryKey(t *testing.T) {
	read := map[string]bool{}
	vars := base()
	if _, err := Load(func(k string) string { read[k] = true; return vars[k] }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, _, ok := strings.Cut(strings.TrimPrefix(line, "#"), "="); ok && strings.HasPrefix(k, "GUNKLY_") && !strings.ContainsAny(k, " \t") {
			documented[k] = true
		}
	}
	for k := range read {
		if !documented[k] {
			t.Errorf(".env.example lacks %s", k)
		}
	}
	for k := range documented {
		if !read[k] {
			t.Errorf(".env.example documents %s, which config.Load does not read", k)
		}
	}
}

func TestLegalAndSecurityTxt(t *testing.T) {
	m := base()
	for k, v := range map[string]string{
		"GUNKLY_HOSTED_MODE": "true", "GUNKLY_EMAIL_ENABLED": "true", "GUNKLY_SMTP_HOST": "h", "GUNKLY_SMTP_FROM": "a@x.example",
		"GUNKLY_BASE_URL": "https://x.example", "GUNKLY_PRIVACY_URL": "https://x.example/privacy", "GUNKLY_TERMS_URL": "https://x.example/terms",
		"GUNKLY_SECURITY_CONTACT": "mailto:security@x.example", "GUNKLY_SECURITY_POLICY": "https://x.example/policy",
	} {
		m[k] = v
	}
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.PrivacyURL != "https://x.example/privacy" || c.TermsURL != "https://x.example/terms" ||
		c.SecurityContact != "mailto:security@x.example" || c.SecurityPolicy != "https://x.example/policy" {
		t.Fatalf("%+v", c)
	}
	// security.txt alone works on a self-hosted instance too.
	m = base()
	m["GUNKLY_SECURITY_CONTACT"] = "https://x.example/report"
	if c, err = Load(env(m)); err != nil || c.SecurityContact != "https://x.example/report" {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestBasePath(t *testing.T) {
	for in, want := range map[string][2]string{
		"https://x.example":           {"https://x.example", ""},
		"https://x.example/":          {"https://x.example", ""},
		"https://x.example/gunkly":    {"https://x.example/gunkly", "/gunkly"},
		"https://x.example/gunkly/":   {"https://x.example/gunkly", "/gunkly"},
		"http://localhost:4865/a/b-c": {"http://localhost:4865/a/b-c", "/a/b-c"},
	} {
		m := base()
		m["GUNKLY_BASE_URL"] = in
		c, err := Load(env(m))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if c.BaseURL != want[0] || c.BasePath() != want[1] {
			t.Errorf("%s: BaseURL %q BasePath %q, want %q %q", in, c.BaseURL, c.BasePath(), want[0], want[1])
		}
	}
}
