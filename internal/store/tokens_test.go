package store_test

import (
	"context"
	"strings"
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

func mkTok(t *testing.T, f *fixture, a store.Actor, hh string) (store.APIToken, string) {
	t.Helper()
	secret := "gk_" + ids.Token()
	tok, err := f.s.CreateAPIToken(context.Background(), a, store.NewAPIToken{Name: "n", TokenHash: ids.Hash(secret),
		Scopes: []string{store.ScopeRead}, HouseholdIDs: []string{hh}, Max: 5})
	noErr(t, err)
	return tok, secret
}

// A password change or reset and removing a second factor revoke every API
// token of the account and audit it with the token ids.
func TestCredentialChangesRevokeAPITokens(t *testing.T) {
	ctx := context.Background()
	steps := map[string]func(f *fixture, u string) error{
		"password": func(f *fixture, u string) error { return f.s.SetPassword(ctx, u, "hash", "") },
		"totp":     func(f *fixture, u string) error { return f.s.DisableTOTP(ctx, u, "") },
		"2fa":      func(f *fixture, u string) error { return f.s.DisableTwoFactor(ctx, u, "") },
		"emailotp": func(f *fixture, u string) error { return f.s.DisableEmailOTP(ctx, u, "") },
	}
	for name, step := range steps {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			t1, s1 := mkTok(t, f, f.m, f.hA)
			t2, s2 := mkTok(t, f, f.m, f.hA)
			_, other := mkTok(t, f, f.g, f.hA)
			noErr(t, step(f, f.m.UserID))
			for _, s := range []string{s1, s2} {
				_, _, err := f.s.APITokenByHash(ctx, ids.Hash(s))
				is(t, err, store.ErrNotFound)
			}
			if _, _, err := f.s.APITokenByHash(ctx, ids.Hash(other)); err != nil {
				t.Fatalf("another user's token revoked: %v", err)
			}
			evs := must(f.s.ListAuditForUser(ctx, f.m.UserID, 50))
			var found bool
			for _, e := range evs {
				if e.Event == store.AuditTokenRevoke && e.Meta["count"] == "2" &&
					strings.Contains(e.Meta["tokens"], t1.ID) && strings.Contains(e.Meta["tokens"], t2.ID) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no token_revoke audit with the ids: %+v", evs)
			}
			// Nothing to revoke: no audit noise.
			noErr(t, step(f, f.m.UserID))
		})
	}
}

// Leaving or being removed deletes the token's binding, so rejoining does not
// revive it; a demotion keeps it.
func TestTokenBindingsDoNotSurviveRemoval(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	_, secret := mkTok(t, f, f.m, f.hA)
	hhs := func() int {
		tok, _, err := f.s.APITokenByHash(ctx, ids.Hash(secret))
		noErr(t, err)
		return len(tok.Households)
	}
	noErr(t, f.s.ChangeRole(ctx, f.a, f.hA, f.m.UserID, store.RoleGuest))
	if hhs() != 1 {
		t.Fatal("demotion dropped the binding")
	}
	noErr(t, f.s.RemoveMember(ctx, f.m, f.hA, f.m.UserID)) // leave
	noErr(t, f.s.AddMember(ctx, f.a, f.hA, f.m.UserID, store.RoleMember))
	if n := hhs(); n != 0 {
		t.Fatalf("binding revived after leave and rejoin: %d", n)
	}
	// Removal by an owner.
	_, secret = mkTok(t, f, f.m, f.hA)
	noErr(t, f.s.RemoveMember(ctx, f.a, f.hA, f.m.UserID))
	noErr(t, f.s.AddMember(ctx, f.a, f.hA, f.m.UserID, store.RoleMember))
	if n := hhs(); n != 0 {
		t.Fatalf("binding revived after removal: %d", n)
	}
}
