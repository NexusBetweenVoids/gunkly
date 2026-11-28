package store_test

import (
	"context"
	"errors"
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

func TestMailOutboxReplacePayload(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	noErr(t, s.EnqueueMail(ctx, store.OutboxMail{ID: "m1", Kind: "otp", Payload: "old", ExpiresAt: now.Add(time.Hour)}, now))
	page, err := s.OutboxSecrets(ctx, "", 10)
	noErr(t, err)
	if len(page) != 1 || page[0].ID != "m1" || page[0].Ciphertext != "old" {
		t.Fatalf("page %+v", page)
	}
	if err := s.ReplaceOutboxPayload(ctx, "m1", "stale", "new"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("changed meanwhile: %v", err)
	}
	noErr(t, s.ReplaceOutboxPayload(ctx, "m1", "old", "new"))
	got, _, err := s.ClaimMail(ctx, now, time.Minute, 10)
	noErr(t, err)
	if len(got) != 1 || got[0].Payload != "new" {
		t.Fatalf("claimed %+v", got)
	}
	if err := s.ReplaceOutboxPayload(ctx, "gone", "old", "new"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sent meanwhile: %v", err)
	}
}
