package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func TestMailOutboxClaimLeaseAndAccountDelete(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a, b := mkUser(t, s, "a"), mkUser(t, s, "b")
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for _, m := range []store.OutboxMail{
		{ID: "m1", Kind: "otp", UserID: a.UserID, Payload: "x", ExpiresAt: now.Add(time.Hour)},
		{ID: "m2", Kind: "verify", UserID: b.UserID, Payload: "y", ExpiresAt: now.Add(time.Hour)},
	} {
		noErr(t, s.EnqueueMail(ctx, m, now))
	}
	got, expired, err := s.ClaimMail(ctx, now, time.Minute, 10)
	noErr(t, err)
	if len(got) != 2 || len(expired) != 0 {
		t.Fatalf("claimed %v expired %v", got, expired)
	}
	// Leased: not handed out again until the lease ends (a crashed sender's claim comes back).
	got, _, err = s.ClaimMail(ctx, now.Add(30*time.Second), time.Minute, 10)
	noErr(t, err)
	if len(got) != 0 {
		t.Fatalf("claimed again during the lease: %v", got)
	}
	got, _, err = s.ClaimMail(ctx, now.Add(61*time.Second), time.Minute, 10)
	noErr(t, err)
	if len(got) != 2 {
		t.Fatalf("not reclaimed after the lease: %v", got)
	}
	// Deleting an account removes its queued mail (the recipient address and text with it).
	_, err = s.DeleteAccount(ctx, a.UserID, store.AccountDeletion{})
	noErr(t, err)
	got, _, err = s.ClaimMail(ctx, now.Add(10*time.Minute), time.Minute, 10)
	noErr(t, err)
	if len(got) != 1 || got[0].ID != "m2" {
		t.Fatalf("after deleting a's account: %v", got)
	}
}
