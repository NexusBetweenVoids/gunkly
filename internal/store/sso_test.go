package store_test

import (
	"context"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

const issuer = "https://auth.example.com"

func TestSSOIdentities(t *testing.T) {
	ctx := context.Background()
	e := newDelEnv(t)
	ann, bob := e.user("ann"), e.user("bob")

	noErr(t, e.s.LinkSSO(ctx, ann.UserID, "", issuer, "sub-ann"))
	is(t, e.s.LinkSSO(ctx, bob.UserID, "", issuer, "sub-ann"), store.ErrConflict)      // one identity, one account
	is(t, e.s.LinkSSO(ctx, ann.UserID, "", issuer, "sub-ann-2"), store.ErrConflict)    // one identity per account and issuer
	noErr(t, e.s.LinkSSO(ctx, bob.UserID, "", "https://other.example.com", "sub-ann")) // the key includes the issuer

	got, err := e.s.SSOIdentityBySubject(ctx, issuer, "sub-ann")
	noErr(t, err)
	if got.UserID != ann.UserID {
		t.Fatal("identity belongs to the wrong account")
	}
	_, err = e.s.SSOIdentityBySubject(ctx, issuer, "nobody")
	is(t, err, store.ErrNotFound)
	_, err = e.s.SSOIdentityForUser(ctx, bob.UserID, issuer)
	is(t, err, store.ErrNotFound)
	u, _ := e.s.UserByID(ctx, ann.UserID)
	if !u.SSOLinked {
		t.Fatal("user not marked as linked")
	}

	// Unlinking only touches the caller's own identity.
	is(t, e.s.UnlinkSSO(ctx, bob.UserID, "", issuer), store.ErrNotFound)
	noErr(t, e.s.UnlinkSSO(ctx, ann.UserID, "", issuer))
	if e.count("SELECT count(*) FROM sso_identities") != 1 {
		t.Fatal("unlink removed another identity")
	}
}

func TestCreateSSOUserHasNoPasswordAndCannotUnlinkWithoutAnotherWay(t *testing.T) {
	ctx := context.Background()
	e := newDelEnv(t)
	u, err := e.s.CreateSSOUser(ctx, store.NewSSOUser{Username: "sso-user", DisplayName: "S", Email: "s@example.org", Issuer: issuer, Subject: "s1"})
	noErr(t, err)
	if u.HasPassword() || !u.EmailVerified || u.PasswordHash != store.NoPassword {
		t.Fatalf("%+v", u)
	}
	got, _ := e.s.UserByID(ctx, u.ID)
	if got.HasPassword() || !got.EmailVerified || got.Email != "s@example.org" {
		t.Fatalf("%+v", got)
	}
	// A taken subject or username creates nothing.
	_, err = e.s.CreateSSOUser(ctx, store.NewSSOUser{Username: "other", DisplayName: "O", Issuer: issuer, Subject: "s1"})
	is(t, err, store.ErrConflict)
	_, err = e.s.CreateSSOUser(ctx, store.NewSSOUser{Username: "SSO-user", DisplayName: "O", Issuer: issuer, Subject: "s2"})
	is(t, err, store.ErrConflict)
	if e.count("SELECT count(*) FROM users") != 1 || e.count("SELECT count(*) FROM sso_identities") != 1 {
		t.Fatal("a failed creation left rows behind")
	}

	// The only way in: unlinking would lock the account out.
	is(t, e.s.UnlinkSSO(ctx, u.ID, "", issuer), store.ErrConflict)
	// A passkey is another way in.
	_, _, err = e.s.AddPasskey(ctx, u.ID, "", newKey("K", "c1"), 5, nil)
	noErr(t, err)
	noErr(t, e.s.UnlinkSSO(ctx, u.ID, "", issuer))
	// So is a password (set later).
	u2, err := e.s.CreateSSOUser(ctx, store.NewSSOUser{Username: "second", DisplayName: "S", Issuer: issuer, Subject: "s3"})
	noErr(t, err)
	noErr(t, e.s.SetPassword(ctx, u2.ID, "real-hash", ""))
	noErr(t, e.s.UnlinkSSO(ctx, u2.ID, "", issuer))
}

func TestAccountDeletionRemovesSSOIdentities(t *testing.T) {
	ctx := context.Background()
	e := newDelEnv(t)
	ann := e.user("ann")
	noErr(t, e.s.LinkSSO(ctx, ann.UserID, "", issuer, "sub-ann"))
	_, err := e.s.DeleteAccount(ctx, ann.UserID, store.AccountDeletion{AutoTransfer: true})
	noErr(t, err)
	if e.count("SELECT count(*) FROM sso_identities") != 0 {
		t.Fatal("identity outlived the account")
	}
}
