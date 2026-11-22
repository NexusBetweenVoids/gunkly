package store_test

import (
	"context"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func TestAPITokens(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	mk := func(a store.Actor, max int, hhs ...string) (store.APIToken, string, error) {
		secret := "gk_" + ids.Token()
		tok, err := f.s.CreateAPIToken(ctx, a, store.NewAPIToken{Name: "n", TokenHash: ids.Hash(secret),
			Scopes: []string{store.ScopeRead}, HouseholdIDs: hhs, Max: max})
		return tok, secret, err
	}
	// Bound only to households the actor belongs to; nothing is stored on failure.
	_, _, err := mk(f.a, 5, f.hA, f.hB)
	is(t, err, store.ErrNotFound)
	_, _, err = mk(f.x, 5, f.hA)
	is(t, err, store.ErrNotFound)
	if ts := must(f.s.ListAPITokens(ctx, f.a.UserID)); len(ts) != 0 {
		t.Fatalf("partial token stored: %v", ts)
	}
	tok, secret, err := mk(f.g, 2, f.hA) // guests may hold tokens
	noErr(t, err)
	got, u, err := f.s.APITokenByHash(ctx, ids.Hash(secret))
	noErr(t, err)
	if got.ID != tok.ID || u.ID != f.g.UserID || len(got.Households) != 1 || got.Households[0].Role != store.RoleGuest {
		t.Fatalf("lookup: %+v %v", got, u.ID)
	}
	// The owner leaving the household drops it from the token.
	noErr(t, f.s.RemoveMember(ctx, f.g, f.hA, f.g.UserID))
	got, _, err = f.s.APITokenByHash(ctx, ids.Hash(secret))
	noErr(t, err)
	if len(got.Households) != 0 {
		t.Fatalf("households after leaving: %v", got.Households)
	}
	// Limit counts active tokens only; revoking frees a slot.
	_, _, err = mk(f.a, 1, f.hA)
	noErr(t, err)
	_, _, err = mk(f.a, 1, f.hA)
	is(t, err, store.ErrLimit)
	// Revocation is the owner's alone, once.
	is(t, f.s.RevokeAPIToken(ctx, f.m.UserID, tok.ID), store.ErrNotFound)
	noErr(t, f.s.RevokeAPIToken(ctx, f.g.UserID, tok.ID))
	is(t, f.s.RevokeAPIToken(ctx, f.g.UserID, tok.ID), store.ErrNotFound)
	_, _, err = f.s.APITokenByHash(ctx, ids.Hash(secret))
	is(t, err, store.ErrNotFound)
}
