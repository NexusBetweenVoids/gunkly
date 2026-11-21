package config

import (
	"encoding/base64"
	"log/slog"
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
	if c.DatabaseURL != "sqlite:///data/gunkly.db" || c.ListenAddr != ":8080" || !c.AutoMigrate ||
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
		"subpath base":     {"GUNKLY_BASE_URL": "https://x.example/gunkly"},
		"bad proxy":        {"GUNKLY_TRUSTED_PROXIES": "10.0.0.0/33"},
		"bad timeout":      {"GUNKLY_SHUTDOWN_TIMEOUT": "-1s"},
		"bad level":        {"GUNKLY_LOG_LEVEL": "loud"},
		"bad port":         {"GUNKLY_SMTP_PORT": "70000"},
		"bad tls":          {"GUNKLY_SMTP_TLS": "ssl3"},
		"email no smtp":    {"GUNKLY_EMAIL_ENABLED": "true"},
		"hosted no email":  {"GUNKLY_HOSTED_MODE": "true", "GUNKLY_BASE_URL": "https://x.example"},
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
