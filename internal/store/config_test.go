package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func TestConfigValues(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	v := func(s string) *string { return &s }
	if err := s.SetConfig(ctx, map[string]*string{"ai.url": v("http://ollama:11434/v1"), "ai.key": v("v1:sealed")}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ConfigValues(ctx)
	if err != nil || got["ai.url"] != "http://ollama:11434/v1" || got["ai.key"] != "v1:sealed" || len(got) != 2 {
		t.Fatalf("got %v %v", got, err)
	}
	// Other settings never show up here.
	open := true
	if err := s.UpdateInstanceSettings(ctx, store.SettingsUpdate{OpenSignup: &open}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConfig(ctx, map[string]*string{"ai.key": nil}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.ConfigValues(ctx); len(got) != 1 || got["ai.key"] != "" {
		t.Fatalf("after delete: %v", got)
	}
	n, err := s.RotateConfigSecrets(ctx, []string{"ai.url", "missing"}, func(k, ct string) (string, error) { return ct + "!", nil })
	if err != nil || n != 1 {
		t.Fatalf("rotate: %d %v", n, err)
	}
	if got, _ = s.ConfigValues(ctx); got["ai.url"] != "http://ollama:11434/v1!" {
		t.Fatalf("after rotate: %v", got)
	}
	if err := s.SetConfig(ctx, map[string]*string{"": v("x")}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("empty key: %v", err)
	}
}
