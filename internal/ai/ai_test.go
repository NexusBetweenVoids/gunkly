package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChat(t *testing.T) {
	var gotAuth, gotPath string
	var gotReq request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL + "/v1/", Model: "m", Key: "sk-1"}
	out, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "x"}}, 10)
	if err != nil || out != "hi" {
		t.Fatalf("%q %v", out, err)
	}
	if gotAuth != "Bearer sk-1" || gotPath != "/v1/chat/completions" || gotReq.Model != "m" || gotReq.Stream || gotReq.MaxTokens != 10 {
		t.Fatalf("request: %q %q %+v", gotAuth, gotPath, gotReq)
	}
	c.Key = ""
	if _, err := c.Chat(context.Background(), nil, 0); err != nil || gotAuth != "" {
		t.Fatalf("no key: %q %v", gotAuth, err)
	}
}

func TestChatErrors(t *testing.T) {
	for status, want := range map[int]error{401: ErrAuth, 403: ErrAuth, 404: ErrNotFound, 429: ErrRateLimited, 500: ErrServer, 200: ErrBadResponse} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"choices":[]}`))
		}))
		c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, Model: "m"}
		_, err := c.Chat(context.Background(), nil, 0)
		if !errors.Is(err, want) || Kind(err) != want.Error() {
			t.Errorf("%d: %v", status, err)
		}
		srv.Close()
	}
	c := &Client{HTTP: http.DefaultClient, BaseURL: "http://127.0.0.1:1", Model: "m"}
	if _, err := c.Chat(context.Background(), nil, 0); Kind(err) != "unreachable" {
		t.Errorf("unreachable: %v", err)
	}
}

func TestJSONObject(t *testing.T) {
	for in, want := range map[string]string{
		"```json\n{\"a\":1}\n```":           `{"a":1}`,
		`Sure! {"a":{"b":"}"}} trailing {}`: `{"a":{"b":"}"}}`,
		`{"s":"a \" { quote"}`:              `{"s":"a \" { quote"}`,
		"no json":                           "",
		`{"open":`:                          "",
	} {
		if got := JSONObject(in); got != want {
			t.Errorf("JSONObject(%q) = %q, want %q", in, got, want)
		}
	}
}
