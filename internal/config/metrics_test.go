package config

import (
	"strings"
	"testing"
)

func TestMetricsToken(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil || c.MetricsToken != "" {
		t.Fatalf("default: %+v %v", c, err)
	}
	m := base()
	m["GUNKLY_METRICS_TOKEN"] = "  0123456789abcdef0123  "
	if c, err = Load(env(m)); err != nil || c.MetricsToken != "0123456789abcdef0123" {
		t.Fatalf("token: %+v %v", c, err)
	}
	m["GUNKLY_METRICS_TOKEN"] = "short-secret"
	if _, err = Load(env(m)); err == nil || strings.Contains(err.Error(), "short-secret") {
		t.Fatalf("short token: %v", err)
	}
}
