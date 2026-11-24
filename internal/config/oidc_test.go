package config

import (
	"reflect"
	"strings"
	"testing"
)

func oidcEnv(extra map[string]string) map[string]string {
	m := base()
	m["GUNKLY_BASE_URL"] = "https://gunkly.example.com"
	m["GUNKLY_OIDC_ISSUER"] = "https://auth.example.com/application/o/gunkly/" // a trailing slash is part of the issuer (Authentik)
	m["GUNKLY_OIDC_CLIENT_ID"] = "gunkly"
	m["GUNKLY_OIDC_CLIENT_SECRET"] = "s3cret with spaces "
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestOIDCOffByDefault(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil || c.OIDC.Issuer != "" {
		t.Fatalf("%v %+v", err, c.OIDC)
	}
}

func TestOIDCConfig(t *testing.T) {
	c, err := Load(env(oidcEnv(map[string]string{"GUNKLY_OIDC_NAME": "Authelia"})))
	if err != nil {
		t.Fatal(err)
	}
	want := OIDC{Issuer: "https://auth.example.com/application/o/gunkly/", ClientID: "gunkly", ClientSecret: "s3cret with spaces ", Name: "Authelia",
		Scopes: []string{"openid", "profile", "email"}}
	if !reflect.DeepEqual(c.OIDC, want) {
		t.Fatalf("%+v", c.OIDC)
	}
	// "openid" is always asked for; scopes are split on whitespace.
	c, err = Load(env(oidcEnv(map[string]string{"GUNKLY_OIDC_SCOPES": "profile  groups"})))
	if err != nil || !reflect.DeepEqual(c.OIDC.Scopes, []string{"openid", "profile", "groups"}) {
		t.Fatalf("%v %+v", err, c.OIDC)
	}
	// A public client has no secret; localhost may use http.
	c, err = Load(env(oidcEnv(map[string]string{"GUNKLY_OIDC_CLIENT_SECRET": "", "GUNKLY_BASE_URL": "http://localhost:4865", "GUNKLY_OIDC_ISSUER": "http://localhost:9091"})))
	if err != nil || c.OIDC.ClientSecret != "" {
		t.Fatalf("%v %+v", err, c.OIDC)
	}
}

func TestOIDCErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		extra map[string]string
		want  string
	}{
		"no client id":    {map[string]string{"GUNKLY_OIDC_CLIENT_ID": ""}, "both required"},
		"no issuer":       {map[string]string{"GUNKLY_OIDC_ISSUER": ""}, "both required"},
		"http issuer":     {map[string]string{"GUNKLY_OIDC_ISSUER": "http://auth.example.com"}, "https"},
		"issuer query":    {map[string]string{"GUNKLY_OIDC_ISSUER": "https://auth.example.com?x=1"}, "https"},
		"issuer userinfo": {map[string]string{"GUNKLY_OIDC_ISSUER": "https://u:p@auth.example.com"}, "https"},
		"no base url":     {map[string]string{"GUNKLY_BASE_URL": ""}, "GUNKLY_BASE_URL"},
		"http base url":   {map[string]string{"GUNKLY_BASE_URL": "http://gunkly.lan:4865"}, "GUNKLY_BASE_URL"},
		"long name":       {map[string]string{"GUNKLY_OIDC_NAME": strings.Repeat("x", 51)}, "GUNKLY_OIDC_NAME"},
		"hosted": {map[string]string{"GUNKLY_HOSTED_MODE": "true", "GUNKLY_EMAIL_ENABLED": "true", "GUNKLY_SMTP_HOST": "smtp.example.com",
			"GUNKLY_SMTP_FROM": "g@example.com"}, "hosted mode"},
	} {
		_, err := Load(env(oidcEnv(tc.extra)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: the error leaks the client secret", name)
		}
	}
}
