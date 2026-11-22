package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func newChannel(max int) store.NewChannel {
	return store.NewChannel{ID: ids.New(), Service: "telegram", Ciphertext: "v1:secret", Label: " Mine ", Max: max}
}

func TestChannels(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	owner, member, guest, stranger := mkUser(t, s, "o"), mkUser(t, s, "m"), mkUser(t, s, "g"), mkUser(t, s, "x")
	hid := mkHousehold(t, s, owner, "H")
	noErr(t, s.AddMember(ctx, owner, hid, member.UserID, store.RoleMember))
	noErr(t, s.AddMember(ctx, owner, hid, guest.UserID, store.RoleGuest))

	// Personal channels: own only, limit enforced.
	c := must(s.CreateUserChannel(ctx, member, newChannel(2)))
	if c.Label != "Mine" || c.Scope != store.ChannelScopeUser || c.UserID != member.UserID {
		t.Fatalf("channel = %+v", c)
	}
	must(s.CreateUserChannel(ctx, member, newChannel(2)))
	_, err := s.CreateUserChannel(ctx, member, newChannel(2))
	is(t, err, store.ErrLimit)
	if n := len(must(s.ListUserChannels(ctx, member))); n != 2 {
		t.Fatalf("%d channels", n)
	}
	if n := len(must(s.ListUserChannels(ctx, stranger))); n != 0 {
		t.Fatal("stranger sees channels")
	}
	l := "x"
	_, err = s.UpdateUserChannel(ctx, stranger, c.ID, store.ChannelUpdate{Label: &l})
	is(t, err, store.ErrNotFound)
	is(t, s.DeleteUserChannel(ctx, stranger, c.ID), store.ErrNotFound)
	must(s.UpdateUserChannel(ctx, member, c.ID, store.ChannelUpdate{Service: "slack", Ciphertext: "v1:new"}))
	_, err = s.UpdateUserChannel(ctx, member, c.ID, store.ChannelUpdate{Service: "slack"})
	is(t, err, store.ErrInvalid)
	noErr(t, s.DeleteUserChannel(ctx, member, c.ID))

	// Household channels: member or owner manage; guests and strangers cannot.
	hc := must(s.CreateHouseholdChannel(ctx, member, hid, newChannel(10)))
	_, err = s.CreateHouseholdChannel(ctx, guest, hid, newChannel(10))
	is(t, err, store.ErrForbidden)
	_, err = s.CreateHouseholdChannel(ctx, stranger, hid, newChannel(10))
	is(t, err, store.ErrNotFound)
	_, err = s.ListHouseholdChannels(ctx, guest, hid)
	is(t, err, store.ErrForbidden)
	if n := len(must(s.ListHouseholdChannels(ctx, owner, hid))); n != 1 {
		t.Fatalf("%d household channels", n)
	}
	is(t, s.DeleteHouseholdChannel(ctx, stranger, hid, hc.ID), store.ErrNotFound)
	noErr(t, s.DeleteHouseholdChannel(ctx, owner, hid, hc.ID))

	// Rotation helpers.
	secrets := must(s.ChannelSecrets(ctx, "", 10))
	if len(secrets) != 1 {
		t.Fatalf("secrets = %+v", secrets)
	}
	is(t, s.ReplaceChannelCiphertext(ctx, secrets[0].ID, "v1:stale", "v2:x"), store.ErrConflict)
	noErr(t, s.ReplaceChannelCiphertext(ctx, secrets[0].ID, secrets[0].Ciphertext, "v2:x"))
	is(t, s.ReplaceChannelCiphertext(ctx, ids.New(), "a", "b"), store.ErrNotFound)
}

func TestMutes(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a, x := mkUser(t, s, "a"), mkUser(t, s, "x")
	hid := mkHousehold(t, s, a, "H")
	c := must(s.CreateChore(ctx, a, hid, choreIn("Dishes")))
	noErr(t, s.SetChoreMuted(ctx, a, hid, c.ID, true))
	noErr(t, s.SetChoreMuted(ctx, a, hid, c.ID, true)) // idempotent
	if m := must(s.MutedChores(ctx, a, hid)); len(m) != 1 || m[0] != c.ID {
		t.Fatalf("muted = %v", m)
	}
	is(t, s.SetChoreMuted(ctx, x, hid, c.ID, true), store.ErrNotFound)
	is(t, s.SetChoreMuted(ctx, a, hid, ids.New(), true), store.ErrNotFound)
	noErr(t, s.SetChoreMuted(ctx, a, hid, c.ID, false))
	if m := must(s.MutedChores(ctx, a, hid)); len(m) != 0 {
		t.Fatalf("muted = %v", m)
	}
}

func TestFeeds(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a, g, x := mkUser(t, s, "a"), mkUser(t, s, "g"), mkUser(t, s, "x")
	hid := mkHousehold(t, s, a, "Home")
	noErr(t, s.AddMember(ctx, a, hid, g.UserID, store.RoleGuest))
	area := must(s.CreateArea(ctx, a, hid, "Garden", 0))
	in := choreIn("Mow")
	in.AreaID, in.Notes, in.Link = area.ID, "secret notes", "https://example.com/private"
	must(s.CreateChore(ctx, a, hid, in))
	archived := must(s.CreateChore(ctx, a, hid, choreIn("Old")))
	noErr(t, s.ArchiveChore(ctx, a, hid, archived.ID))

	f, tok := must2(s.CreateFeed(ctx, g, hid, 2)) // guests may have feeds
	if d := must(s.FeedByToken(ctx, tok)); d.Chores[0].AreaName != "" {
		t.Fatalf("area name with areas off: %+v", d.Chores[0])
	}
	on := true
	must(s.UpdateHousehold(ctx, a, hid, store.HouseholdUpdate{AreasEnabled: &on}))
	data := must(s.FeedByToken(ctx, tok))
	if data.HouseholdName != "Home" || len(data.Chores) != 1 || data.Chores[0].AreaName != "Garden" ||
		data.Chores[0].Notes != "" || data.Chores[0].Link != "" || data.Location.String() != "Europe/Amsterdam" {
		t.Fatalf("feed = %+v", data)
	}
	_, _, err := s.CreateFeed(ctx, x, hid, 2)
	is(t, err, store.ErrNotFound)
	must2(s.CreateFeed(ctx, g, hid, 2))
	_, _, err = s.CreateFeed(ctx, g, hid, 2)
	is(t, err, store.ErrLimit)
	if n := len(must(s.ListFeeds(ctx, g, hid))); n != 2 {
		t.Fatalf("%d feeds", n)
	}

	tok2 := must(s.RegenerateFeed(ctx, g, hid, f.ID))
	_, err = s.FeedByToken(ctx, tok)
	is(t, err, store.ErrNotFound)
	must(s.FeedByToken(ctx, tok2))
	_, err = s.RegenerateFeed(ctx, x, hid, f.ID)
	is(t, err, store.ErrNotFound)
	is(t, s.RevokeFeed(ctx, a, hid, f.ID), store.ErrNotFound) // someone else's feed
	noErr(t, s.RevokeFeed(ctx, g, hid, f.ID))
	_, err = s.FeedByToken(ctx, tok2)
	is(t, err, store.ErrNotFound)

	// Leaving the household kills the feed.
	_, tok3 := must2(s.CreateFeed(ctx, g, hid, 5))
	noErr(t, s.RemoveMember(ctx, g, hid, g.UserID))
	_, err = s.FeedByToken(ctx, tok3)
	is(t, err, store.ErrNotFound)
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

func TestDeliveriesEnqueueClaimAndLog(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a, b := mkUser(t, s, "a"), mkUser(t, s, "b")
	hid := mkHousehold(t, s, a, "H")
	noErr(t, s.AddMember(ctx, a, hid, b.UserID, store.RoleMember))
	c := must(s.CreateChore(ctx, a, hid, choreIn("Dishes")))
	ch := must(s.CreateUserChannel(ctx, a, newChannel(10)))
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	d := store.NewDelivery{Key: "k1", HouseholdID: hid, ChoreID: c.ID, UserID: a.UserID, ChannelID: ch.ID,
		Kind: schedule.OnDue, DueDate: c.DueDate, FireAt: now, NextAttemptAt: now}
	if n := must(s.EnqueueDeliveries(ctx, []store.NewDelivery{d, d}, store.Quota{}, now)); n != 1 {
		t.Fatalf("enqueued %d", n)
	}
	if n := must(s.EnqueueDeliveries(ctx, []store.NewDelivery{d}, store.Quota{}, now)); n != 0 {
		t.Fatal("duplicate key enqueued")
	}
	// Coalescing upgrades the kind of a pending delivery.
	q := d
	q.Key, q.Coalesce, q.Kind = "quiet", true, schedule.Before
	must(s.EnqueueDeliveries(ctx, []store.NewDelivery{q}, store.Quota{}, now))
	q.Kind = schedule.Overdue
	must(s.EnqueueDeliveries(ctx, []store.NewDelivery{q}, store.Quota{}, now))

	got := must(s.ClaimDeliveries(ctx, now, time.Minute, 10))
	if len(got) != 2 {
		t.Fatalf("claimed %d", len(got))
	}
	for _, g := range got {
		if !g.ChoreActive || !g.RecipientOK || g.Service != "telegram" || g.Ciphertext != "v1:secret" || g.ChoreName != "Dishes" {
			t.Fatalf("claimed = %+v", g)
		}
	}
	if got[1].Kind != schedule.Overdue && got[0].Kind != schedule.Overdue {
		t.Fatalf("coalesced kind not upgraded: %+v", got)
	}
	// Leased: not claimable again until the lease ends.
	if n := len(must(s.ClaimDeliveries(ctx, now, time.Minute, 10))); n != 0 {
		t.Fatalf("re-claimed %d", n)
	}
	retry := now.Add(5 * time.Minute)
	noErr(t, s.MarkDeliveryFailed(ctx, got[0].ID, "boom", retry))
	noErr(t, s.MarkDeliverySent(ctx, got[1].ID, now))
	if n := len(must(s.ClaimDeliveries(ctx, retry.Add(-time.Second), time.Minute, 10))); n != 0 {
		t.Fatal("claimed before backoff elapsed")
	}
	again := must(s.ClaimDeliveries(ctx, retry, time.Minute, 10))
	if len(again) != 1 || again[0].Attempts != 1 {
		t.Fatalf("retry claim = %+v", again)
	}
	noErr(t, s.MarkDeliveryFailed(ctx, again[0].ID, "boom", time.Time{}))

	log := must(s.ListUserDeliveries(ctx, a, 10))
	if len(log) != 2 {
		t.Fatalf("log = %+v", log)
	}
	statuses := map[string]int{}
	for _, l := range log {
		statuses[l.Status] = l.Attempts
	}
	if statuses[store.DeliverySent] != 1 || statuses[store.DeliveryFailed] != 2 {
		t.Fatalf("statuses = %v", statuses)
	}
	// Household log shows b nothing of a's personal deliveries; guests/strangers none.
	if n := len(must(s.ListHouseholdDeliveries(ctx, b, hid, 10))); n != 0 {
		t.Fatalf("b sees %d", n)
	}
	if n := len(must(s.ListHouseholdDeliveries(ctx, a, hid, 10))); n != 2 {
		t.Fatalf("a sees %d", n)
	}
	if n := len(must(s.ListUserDeliveries(ctx, b, 10))); n != 0 {
		t.Fatal("b sees a's log")
	}
}

func TestDeliveryQuota(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a := mkUser(t, s, "a")
	hid := mkHousehold(t, s, a, "H")
	c := must(s.CreateChore(ctx, a, hid, choreIn("Dishes")))
	ch := must(s.CreateUserChannel(ctx, a, newChannel(10)))
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	var ds []store.NewDelivery
	for i := range 5 {
		ds = append(ds, store.NewDelivery{Key: "k" + string(rune('a'+i)), HouseholdID: hid, ChoreID: c.ID, UserID: a.UserID,
			ChannelID: ch.ID, Kind: schedule.OnDue, DueDate: c.DueDate, FireAt: now, NextAttemptAt: now})
	}
	if n := must(s.EnqueueDeliveries(ctx, ds, store.Quota{HouseholdHour: 3, HouseholdDay: 10, UserDay: 10}, now)); n != 3 {
		t.Fatalf("queued %d, want 3", n)
	}
	failed := 0
	for _, d := range must(s.ListUserDeliveries(ctx, a, 10)) {
		if d.Status == store.DeliveryFailed && d.LastError == store.QuotaExceeded {
			failed++
		}
	}
	if failed != 2 {
		t.Fatalf("%d over-quota deliveries logged", failed)
	}
	// Per-user daily cap.
	ds2 := []store.NewDelivery{ds[0]}
	ds2[0].Key = "user-cap"
	if n := must(s.EnqueueDeliveries(ctx, ds2, store.Quota{UserDay: 3}, now.Add(2*time.Hour))); n != 0 {
		t.Fatal("user cap not enforced")
	}
}

func TestPurgeExpired(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	a := mkUser(t, s, "a")
	hid := mkHousehold(t, s, a, "H")
	c := must(s.CreateChore(ctx, a, hid, choreIn("Dishes")))
	ch := must(s.CreateUserChannel(ctx, a, newChannel(10)))
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := old.Add(31 * 24 * time.Hour)
	d := store.NewDelivery{Key: "old", HouseholdID: hid, ChoreID: c.ID, UserID: a.UserID, ChannelID: ch.ID, Kind: schedule.OnDue,
		DueDate: c.DueDate, FireAt: old, NextAttemptAt: old}
	must(s.EnqueueDeliveries(ctx, []store.NewDelivery{d}, store.Quota{}, old))
	d.Key = "new"
	must(s.EnqueueDeliveries(ctx, []store.NewDelivery{d}, store.Quota{}, now))
	noErr(t, s.Audit(ctx, store.AuditEvent{ActorUserID: a.UserID, Event: store.AuditLogout}))
	n := must(s.PurgeExpired(ctx, store.InstanceSettings{AuditRetentionDays: 90, DeliveryRetentionDays: 30}.Policy(), now))
	if n < 1 {
		t.Fatalf("purged %d", n)
	}
	log := must(s.ListUserDeliveries(ctx, a, 10))
	if len(log) != 1 {
		t.Fatalf("deliveries left: %d", len(log))
	}
	if len(must(s.ListAuditForUser(ctx, a.UserID, 10))) != 1 {
		t.Fatal("recent audit entry purged")
	}
	noErr(t, s.ReclaimSpace(ctx, 100, 3))
}
