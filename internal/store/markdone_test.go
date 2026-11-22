package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// mdFixture: household h with owner, member m, guest g, one chore and a
// household channel; the store clock is controllable.
type mdFixture struct {
	s          *store.Store
	now        time.Time
	mu         sync.Mutex
	owner, m   store.Actor
	g          store.Actor
	hid        string
	chore      store.Chore
	channelID  string
	ctx        context.Context
	otherHID   string
	otherChore store.Chore
}

func (f *mdFixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *mdFixture) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func newMD(t *testing.T) *mdFixture {
	t.Helper()
	f := &mdFixture{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC), ctx: context.Background()}
	f.s = store.New(dbtest.Open(t)).WithClock(f.clock)
	f.owner, f.m, f.g = mkUser(t, f.s, "owner"), mkUser(t, f.s, "member"), mkUser(t, f.s, "guest")
	f.hid = mkHousehold(t, f.s, f.owner, "Home")
	noErr(t, f.s.AddMember(f.ctx, f.owner, f.hid, f.m.UserID, store.RoleMember))
	noErr(t, f.s.AddMember(f.ctx, f.owner, f.hid, f.g.UserID, store.RoleGuest))
	f.chore = must(f.s.CreateChore(f.ctx, f.owner, f.hid, choreIn("Gutters")))
	ch := must(f.s.CreateHouseholdChannel(f.ctx, f.m, f.hid, store.NewChannel{ID: ids.New(), Service: "discord", Ciphertext: "v1:x", Label: "Family", Max: 10}))
	f.channelID = ch.ID
	stranger := mkUser(t, f.s, "stranger")
	f.otherHID = mkHousehold(t, f.s, stranger, "Other")
	f.otherChore = must(f.s.CreateChore(f.ctx, stranger, f.otherHID, choreIn("Other chore")))
	return f
}

func (f *mdFixture) link(t *testing.T, userID, channelID string) string {
	t.Helper()
	return must(f.s.CreateMarkDone(f.ctx, store.MarkDoneLink{ChoreID: f.chore.ID, OccurrenceDue: f.chore.NextDue,
		UserID: userID, ChannelID: channelID, ExpiresAt: f.now.Add(store.MarkDoneMaxTTL)}))
}

func TestMarkDonePeekConsumeUndo(t *testing.T) {
	f := newMD(t)
	tok := f.link(t, f.m.UserID, "")
	for range 3 { // GET (and link previews) change nothing
		if name, err := f.s.PeekMarkDone(f.ctx, tok); err != nil || name != "Gutters" {
			t.Fatalf("Peek = %q, %v", name, err)
		}
	}
	if c := must(f.s.Chore(f.ctx, f.owner, f.hid, f.chore.ID)); c.DueDate != f.chore.DueDate {
		t.Fatal("peek changed the chore")
	}
	res := must(f.s.ConsumeMarkDone(f.ctx, tok))
	if res.ChoreName != "Gutters" || res.UserID != f.m.UserID || res.UndoToken == "" || !res.UndoExpiresAt.Equal(f.now.Add(store.UndoTTL)) {
		t.Fatalf("result = %+v", res)
	}
	c := must(f.s.Chore(f.ctx, f.owner, f.hid, f.chore.ID))
	if c.DueDate != "2026-11-28" { // weekly, from completion on 2026-11-21
		t.Fatalf("due after completion = %s", c.DueDate)
	}
	hist := must(f.s.ListCompletions(f.ctx, f.owner, f.hid, f.chore.ID, 0))
	if len(hist) != 1 || hist[0].Source != store.SourceMarkDoneLink || hist[0].UserID != f.m.UserID {
		t.Fatalf("history = %+v", hist)
	}
	// Single use.
	_, err := f.s.ConsumeMarkDone(f.ctx, tok)
	is(t, err, store.ErrNotFound)
	_, err = f.s.PeekMarkDone(f.ctx, tok)
	is(t, err, store.ErrNotFound)

	// Undo: scoped to the completion, single use.
	u := must(f.s.UndoWithToken(f.ctx, res.UndoToken))
	if u.CompletionID != res.CompletionID || u.HouseholdID != f.hid {
		t.Fatalf("undo = %+v", u)
	}
	if c := must(f.s.Chore(f.ctx, f.owner, f.hid, f.chore.ID)); c.DueDate != f.chore.DueDate || !c.NextDue.Equal(f.chore.NextDue) {
		t.Fatalf("undo did not restore: %s", c.DueDate)
	}
	_, err = f.s.UndoWithToken(f.ctx, res.UndoToken)
	is(t, err, store.ErrNotFound)
}

func TestUndoTokenExpires(t *testing.T) {
	f := newMD(t)
	res := must(f.s.ConsumeMarkDone(f.ctx, f.link(t, f.m.UserID, "")))
	f.advance(store.UndoTTL)
	_, err := f.s.UndoWithToken(f.ctx, res.UndoToken)
	is(t, err, store.ErrNotFound)
	_, err = f.s.UndoWithToken(f.ctx, "garbage")
	is(t, err, store.ErrNotFound)
}

func TestMarkDoneExpiry(t *testing.T) {
	f := newMD(t)
	tok := f.link(t, f.m.UserID, "")
	f.advance(store.MarkDoneMaxTTL - time.Millisecond)
	must(f.s.PeekMarkDone(f.ctx, tok))
	f.advance(time.Millisecond)
	_, err := f.s.PeekMarkDone(f.ctx, tok)
	is(t, err, store.ErrNotFound)
	_, err = f.s.ConsumeMarkDone(f.ctx, tok)
	is(t, err, store.ErrNotFound)
	// TTL over 7 days is refused.
	_, err = f.s.CreateMarkDone(f.ctx, store.MarkDoneLink{ChoreID: f.chore.ID, OccurrenceDue: f.chore.NextDue,
		UserID: f.m.UserID, ExpiresAt: f.now.Add(store.MarkDoneMaxTTL + time.Second)})
	is(t, err, store.ErrInvalid)
}

func TestMarkDoneHouseholdChannel(t *testing.T) {
	f := newMD(t)
	res := must(f.s.ConsumeMarkDone(f.ctx, f.link(t, "", f.channelID)))
	if res.UserID != "" || res.ChannelID != f.channelID {
		t.Fatalf("res = %+v", res)
	}
	hist := must(f.s.ListCompletions(f.ctx, f.owner, f.hid, f.chore.ID, 0))
	if hist[0].UserID != "" || hist[0].TokenID != f.channelID || hist[0].Source != store.SourceMarkDoneLink {
		t.Fatalf("history = %+v", hist)
	}
}

func TestMarkDoneIssueRules(t *testing.T) {
	f := newMD(t)
	exp := f.now.Add(time.Hour)
	for name, l := range map[string]store.MarkDoneLink{
		"guest recipient":         {ChoreID: f.chore.ID, OccurrenceDue: f.chore.NextDue, UserID: f.g.UserID, ExpiresAt: exp},
		"non-member recipient":    {ChoreID: f.otherChore.ID, OccurrenceDue: f.otherChore.NextDue, UserID: f.m.UserID, ExpiresAt: exp},
		"other household channel": {ChoreID: f.otherChore.ID, OccurrenceDue: f.otherChore.NextDue, ChannelID: f.channelID, ExpiresAt: exp},
		"stale occurrence":        {ChoreID: f.chore.ID, OccurrenceDue: f.chore.NextDue.Add(-24 * time.Hour), UserID: f.m.UserID, ExpiresAt: exp},
		"both targets":            {ChoreID: f.chore.ID, OccurrenceDue: f.chore.NextDue, UserID: f.m.UserID, ChannelID: f.channelID, ExpiresAt: exp},
		"no target":               {ChoreID: f.chore.ID, OccurrenceDue: f.chore.NextDue, ExpiresAt: exp},
	} {
		if _, err := f.s.CreateMarkDone(f.ctx, l); err == nil {
			t.Errorf("%s: issued", name)
		}
	}
}

func TestMarkDoneInvalidation(t *testing.T) {
	cosmetic := func(f *mdFixture) store.ChoreInput {
		in := choreIn("Gutters, renamed")
		in.Notes, in.Link = "new notes", "https://example.com"
		in.Assignees = []string{f.m.UserID}
		return in
	}
	cases := []struct {
		name    string
		channel bool // household-channel link instead of personal
		act     func(t *testing.T, f *mdFixture)
		valid   bool
	}{
		{"completion by anyone", false, func(t *testing.T, f *mdFixture) {
			in := store.CompletionInput{CompletedAt: f.now, Source: store.SourceApp, DueDate: "2026-11-28", NextDue: f.now.Add(7 * 24 * time.Hour)}
			must(f.s.CompleteChore(f.ctx, f.owner, f.hid, f.chore.ID, in))
		}, false},
		{"due change / snooze / skip", false, func(t *testing.T, f *mdFixture) {
			in := choreIn("Gutters")
			in.DueDate, in.NextDue = "2026-11-23", f.chore.NextDue.Add(48*time.Hour)
			must(f.s.UpdateChore(f.ctx, f.owner, f.hid, f.chore.ID, in))
		}, false},
		{"recurrence change", false, func(t *testing.T, f *mdFixture) {
			in := choreIn("Gutters")
			in.IntervalN = 2
			must(f.s.UpdateChore(f.ctx, f.owner, f.hid, f.chore.ID, in))
		}, false},
		{"pause", false, func(t *testing.T, f *mdFixture) {
			in := choreIn("Gutters")
			in.PausedIndefinitely = true
			must(f.s.UpdateChore(f.ctx, f.owner, f.hid, f.chore.ID, in))
		}, false},
		{"archive", false, func(t *testing.T, f *mdFixture) { noErr(t, f.s.ArchiveChore(f.ctx, f.owner, f.hid, f.chore.ID)) }, false},
		{"delete", false, func(t *testing.T, f *mdFixture) { noErr(t, f.s.DeleteChore(f.ctx, f.owner, f.hid, f.chore.ID)) }, false},
		{"password change", false, func(t *testing.T, f *mdFixture) { noErr(t, f.s.SetPassword(f.ctx, f.m.UserID, "new-hash", "")) }, false},
		{"downgrade to guest", false, func(t *testing.T, f *mdFixture) {
			noErr(t, f.s.ChangeRole(f.ctx, f.owner, f.hid, f.m.UserID, store.RoleGuest))
		}, false},
		{"leaving", false, func(t *testing.T, f *mdFixture) { noErr(t, f.s.RemoveMember(f.ctx, f.m, f.hid, f.m.UserID)) }, false},
		{"household channel edit", true, func(t *testing.T, f *mdFixture) {
			l := "Renamed"
			must(f.s.UpdateHouseholdChannel(f.ctx, f.owner, f.hid, f.channelID, store.ChannelUpdate{Label: &l}))
		}, false},
		{"household channel delete", true, func(t *testing.T, f *mdFixture) {
			noErr(t, f.s.DeleteHouseholdChannel(f.ctx, f.owner, f.hid, f.channelID))
		}, false},
		{"cosmetic edit", false, func(t *testing.T, f *mdFixture) {
			must(f.s.UpdateChore(f.ctx, f.owner, f.hid, f.chore.ID, cosmetic(f)))
		}, true},
		{"cosmetic edit, channel link", true, func(t *testing.T, f *mdFixture) {
			must(f.s.UpdateChore(f.ctx, f.owner, f.hid, f.chore.ID, cosmetic(f)))
		}, true},
		{"other user's password change", false, func(t *testing.T, f *mdFixture) { noErr(t, f.s.SetPassword(f.ctx, f.owner.UserID, "new-hash", "")) }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newMD(t)
			user, channel := f.m.UserID, ""
			if c.channel {
				user, channel = "", f.channelID
			}
			tok := f.link(t, user, channel)
			c.act(t, f)
			_, err := f.s.PeekMarkDone(f.ctx, tok)
			if c.valid {
				noErr(t, err)
				must(f.s.ConsumeMarkDone(f.ctx, tok))
				return
			}
			is(t, err, store.ErrNotFound)
			_, err = f.s.ConsumeMarkDone(f.ctx, tok)
			is(t, err, store.ErrNotFound)
		})
	}
}

// Concurrent POSTs of the same token: exactly one completes.
func TestMarkDoneConcurrentConsume(t *testing.T) {
	f := newMD(t)
	tok := f.link(t, f.m.UserID, "")
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.s.ConsumeMarkDone(f.ctx, tok); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d consumes succeeded", ok)
	}
	if n := len(must(f.s.ListCompletions(f.ctx, f.owner, f.hid, f.chore.ID, 0))); n != 1 {
		t.Fatalf("%d completions", n)
	}
}

// Two different links for the same occurrence, consumed concurrently, must
// produce exactly one completion.
func TestMarkDoneTwoTokensOneCompletion(t *testing.T) {
	f := newMD(t)
	t1 := f.link(t, f.m.UserID, "")
	t2 := f.link(t, f.owner.UserID, "")
	var wg sync.WaitGroup
	var mu sync.Mutex
	okN := 0
	for _, tok := range []string{t1, t2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.s.ConsumeMarkDone(f.ctx, tok); err == nil {
				mu.Lock()
				okN++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	hist := must(f.s.ListCompletions(f.ctx, f.owner, f.hid, f.chore.ID, 0))
	if okN != 1 || len(hist) != 1 {
		t.Fatalf("%d consumed, %d completions, want exactly 1", okN, len(hist))
	}
}
