package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func TestOAuthClientRegistrationCaps(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	uris := []string{"https://app.example/cb"}
	mk := func(max, per int, key string) (store.OAuthClient, error) {
		return s.CreateOAuthClient(ctx, "app", uris, max, per, key)
	}
	// Per registering key: unused clients are capped, other keys are not affected.
	for range 2 {
		if _, err := mk(100, 2, "1.2.3.4"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mk(100, 2, "1.2.3.4"); !errors.Is(err, store.ErrLimit) {
		t.Fatalf("third unused client of one key: %v", err)
	}
	if _, err := mk(100, 2, "2001:db8::"); err != nil {
		t.Fatalf("another key: %v", err)
	}
	// At the instance cap the oldest unused client is evicted...
	all := 3
	c, err := mk(all, 2, "9.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	// ...but not one that has been approved: with every client in use it fails.
	a := mkUser(t, s, "u")
	h := mkHousehold(t, s, a, "H")
	approve := func(id string) {
		t.Helper()
		if _, err := s.CreateOAuthCode(ctx, a, store.NewOAuthCode{ClientID: id, RedirectURI: uris[0], Challenge: "c", CodeHash: id,
			Scopes: []string{"chores:read"}, HouseholdIDs: []string{h}, Max: 50}); err != nil {
			t.Fatal(err)
		}
	}
	approve(c.ID)
	d, err := mk(all, 2, "8.8.8.8") // evicts an unused one
	if err != nil {
		t.Fatal(err)
	}
	approve(d.ID)
	e, err := mk(all, 2, "7.7.7.7") // the last unused one makes room
	if err != nil {
		t.Fatal(err)
	}
	approve(e.ID)
	if _, err := mk(all, 2, "6.6.6.6"); !errors.Is(err, store.ErrLimit) {
		t.Fatalf("all clients in use: %v", err)
	}
	if _, err := s.OAuthClientByID(ctx, c.ID); err != nil {
		t.Fatalf("an approved client was evicted: %v", err)
	}
}
