package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// TestResetPasswordInvalidatesLinks: `gunkly admin reset-password` must kill
// the user's mark-done links like the API paths do.
func TestResetPasswordInvalidatesLinks(t *testing.T) {
	ctx := context.Background()
	d := dbtest.Open(t)
	st := store.New(d)
	u, err := st.CreateUser(ctx, store.NewUser{Username: "ann", DisplayName: "Ann", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	actor := store.Actor{UserID: u.ID}
	h, err := st.CreateHousehold(ctx, actor, store.NewHousehold{Name: "Home", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	next := time.Now().Add(time.Hour).UTC().Truncate(time.Millisecond)
	c, err := st.CreateChore(ctx, actor, h.ID, store.ChoreInput{Name: "Gutters", Kind: store.KindRecurring, ScheduleType: store.ScheduleInterval, IntervalN: 1,
		IntervalUnit: "week", Anchor: store.AnchorFromCompletion, FirstDue: "2026-11-21", DueDate: "2026-11-21", NextDue: next})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := st.CreateMarkDone(ctx, store.MarkDoneLink{ChoreID: c.ID, OccurrenceDue: next, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PeekMarkDone(ctx, tok); err != nil {
		t.Fatalf("link not valid before reset: %v", err)
	}
	a := &app{db: d, store: st}
	var out bytes.Buffer
	if err := a.adminCommand(ctx, "reset-password", "ann", &out); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PeekMarkDone(ctx, tok); err == nil {
		t.Fatal("mark-done link survived the CLI password reset")
	}
}
