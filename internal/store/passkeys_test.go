package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func newKey(name, id string) store.NewPasskey {
	return store.NewPasskey{Name: name, CredentialID: []byte(id), PublicKey: []byte("pub-" + id), Transports: []string{"internal", "hybrid"},
		AAGUID: make([]byte, 16), Flags: 0x45}
}

func TestPasskeyLifecycle(t *testing.T) {
	ctx := context.Background()
	e := newDelEnv(t)
	ann, bob := e.user("ann"), e.user("bob")

	p, issued, err := e.s.AddPasskey(ctx, ann.UserID, "", newKey("Laptop", "c1"), 2, []string{"h1", "h2"})
	noErr(t, err)
	if !issued || p.ID == "" {
		t.Fatalf("first factor issues recovery codes: %v %v", issued, p)
	}
	if e.count("SELECT count(*) FROM recovery_codes WHERE user_id = ?", ann.UserID) != 2 {
		t.Fatal("recovery codes not stored")
	}
	_, issued, err = e.s.AddPasskey(ctx, ann.UserID, "", newKey("Phone", "c2"), 2, []string{"h3"})
	noErr(t, err)
	if issued {
		t.Fatal("a second passkey must not replace the recovery codes")
	}
	_, _, err = e.s.AddPasskey(ctx, ann.UserID, "", newKey("Third", "c3"), 2, nil)
	is(t, err, store.ErrLimit)
	_, _, err = e.s.AddPasskey(ctx, bob.UserID, "", newKey("Same credential", "c1"), 2, nil)
	is(t, err, store.ErrConflict)

	keys, err := e.s.ListPasskeys(ctx, ann.UserID)
	noErr(t, err)
	// Both keys are created at the same (test clock) instant, so the order falls back to the random ID bits.
	lap := slices.IndexFunc(keys, func(k store.Passkey) bool { return k.Name == "Laptop" })
	if len(keys) != 2 || lap < 0 || string(keys[lap].CredentialID) != "c1" || string(keys[lap].PublicKey) != "pub-c1" ||
		len(keys[lap].Transports) != 2 || len(keys[lap].AAGUID) != 16 || keys[lap].Flags != 0x45 {
		t.Fatalf("round trip: %+v", keys)
	}
	if other, _ := e.s.ListPasskeys(ctx, bob.UserID); len(other) != 0 {
		t.Fatal("bob sees ann's passkeys")
	}
	u, err := e.s.UserByID(ctx, ann.UserID)
	noErr(t, err)
	if u.Passkeys != 2 || !u.HasSecondFactor() {
		t.Fatalf("user: %+v", u)
	}

	found, err := e.s.PasskeyByCredentialID(ctx, []byte("c2"))
	noErr(t, err)
	if found.UserID != ann.UserID {
		t.Fatal("lookup by credential ID")
	}
	_, err = e.s.PasskeyByCredentialID(ctx, []byte("nope"))
	is(t, err, store.ErrNotFound)

	// Another user's passkey cannot be deleted (isolation).
	is(t, e.s.DeletePasskey(ctx, bob.UserID, "", keys[0].ID, false), store.ErrNotFound)
	// Hosted: the last second factor stays; here the second passkey may go.
	noErr(t, e.s.DeletePasskey(ctx, ann.UserID, "", keys[1].ID, true))
	is(t, e.s.DeletePasskey(ctx, ann.UserID, "", keys[0].ID, true), store.ErrConflict)
	noErr(t, e.s.DeletePasskey(ctx, ann.UserID, "", keys[0].ID, false))
	if e.count("SELECT count(*) FROM recovery_codes WHERE user_id = ?", ann.UserID) != 0 {
		t.Fatal("recovery codes must go with the last second factor")
	}
}

func TestUsePasskeySignCount(t *testing.T) {
	ctx := context.Background()
	e := newDelEnv(t)
	ann := e.user("ann")
	p, _, err := e.s.AddPasskey(ctx, ann.UserID, "", newKey("K", "c1"), 5, nil)
	noErr(t, err)
	noErr(t, e.s.UsePasskey(ctx, ann.UserID, p.ID, 4, 0x45))
	noErr(t, e.s.UsePasskey(ctx, ann.UserID, p.ID, 5, 0x45))
	is(t, e.s.UsePasskey(ctx, ann.UserID, p.ID, 5, 0x45), store.ErrConflict)
	is(t, e.s.UsePasskey(ctx, ann.UserID, p.ID, 2, 0x45), store.ErrConflict)
	got, _ := e.s.ListPasskeys(ctx, ann.UserID)
	if got[0].SignCount != 5 || got[0].LastUsedAt.IsZero() {
		t.Fatalf("%+v", got[0])
	}
	// An authenticator that never counts stays at zero.
	z, _, err := e.s.AddPasskey(ctx, ann.UserID, "", newKey("Z", "c2"), 5, nil)
	noErr(t, err)
	noErr(t, e.s.UsePasskey(ctx, ann.UserID, z.ID, 0, 0x45))
	noErr(t, e.s.UsePasskey(ctx, ann.UserID, z.ID, 0, 0x45))
	// Another user's passkey is not touched.
	bob := e.user("bob")
	is(t, e.s.UsePasskey(ctx, bob.UserID, p.ID, 99, 0x45), store.ErrConflict)
}

func TestPasskeyChallengeSingleUseBoundAndExpiring(t *testing.T) {
	ctx := context.Background()
	e := newDelEnv(t)
	ann, bob := e.user("ann"), e.user("bob")
	id, err := e.s.SavePasskeyChallenge(ctx, store.PasskeyRegister, ann.UserID, `{"x":1}`)
	noErr(t, err)
	// Wrong user or purpose spends it as well.
	_, err = e.s.TakePasskeyChallenge(ctx, id, store.PasskeyRegister, bob.UserID)
	is(t, err, store.ErrNotFound)
	_, err = e.s.TakePasskeyChallenge(ctx, id, store.PasskeyRegister, ann.UserID)
	is(t, err, store.ErrNotFound)

	id, _ = e.s.SavePasskeyChallenge(ctx, store.PasskeyLogin, "", `{"y":2}`)
	data, err := e.s.TakePasskeyChallenge(ctx, id, store.PasskeyLogin, "")
	noErr(t, err)
	if data != `{"y":2}` {
		t.Fatal(data)
	}
	_, err = e.s.TakePasskeyChallenge(ctx, id, store.PasskeyLogin, "")
	is(t, err, store.ErrNotFound)

	id, _ = e.s.SavePasskeyChallenge(ctx, store.PasskeySecond, ann.UserID, `{}`)
	e.clk.t = e.clk.t.Add(6 * time.Minute)
	_, err = e.s.TakePasskeyChallenge(ctx, id, store.PasskeySecond, ann.UserID)
	is(t, err, store.ErrNotFound)
	// Saving cleans up expired rows.
	_, _ = e.s.SavePasskeyChallenge(ctx, store.PasskeyLogin, "", `{}`)
	if e.count("SELECT count(*) FROM passkey_challenges") != 1 {
		t.Fatal("expired challenges were not purged")
	}
}

func TestResetTwoFactorRemovesPasskeysAndAccountDeletionToo(t *testing.T) {
	ctx := context.Background()
	e := newDelEnv(t)
	ann, bob := e.user("ann"), e.user("bob")
	for _, a := range []store.Actor{ann, bob} {
		_, _, err := e.s.AddPasskey(ctx, a.UserID, "", newKey("K", "c-"+a.UserID), 5, []string{"h-" + a.UserID})
		noErr(t, err)
	}
	_, err := e.s.SavePasskeyChallenge(ctx, store.PasskeyStepUp, bob.UserID, `{}`)
	noErr(t, err)
	noErr(t, e.s.DisableTwoFactor(ctx, ann.UserID, ""))
	if e.count("SELECT count(*) FROM passkeys WHERE user_id = ?", ann.UserID) != 0 ||
		e.count("SELECT count(*) FROM recovery_codes WHERE user_id = ?", ann.UserID) != 0 {
		t.Fatal("reset-2fa left passkeys or recovery codes")
	}
	if e.count("SELECT count(*) FROM passkeys WHERE user_id = ?", bob.UserID) != 1 {
		t.Fatal("reset touched another account")
	}
	u, _ := e.s.UserByID(ctx, ann.UserID)
	if u.HasSecondFactor() {
		t.Fatal("still has a second factor")
	}
	_, err = e.s.DeleteAccount(ctx, bob.UserID, store.AccountDeletion{AutoTransfer: true})
	noErr(t, err)
	if e.count("SELECT count(*) FROM passkeys") != 0 || e.count("SELECT count(*) FROM passkey_challenges") != 0 {
		t.Fatal("account deletion left passkey rows")
	}
}
