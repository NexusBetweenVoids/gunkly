package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func TestAreaCapIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := mkUser(t, s, "a")
	hh := mkHousehold(t, s, a, "H")
	var wg sync.WaitGroup
	var mu sync.Mutex
	okN := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.CreateArea(ctx, a, hh, fmt.Sprintf("Area %d", i), 5); err == nil {
				mu.Lock()
				okN++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okN != 5 || len(must(s.ListAreas(ctx, a, hh))) != 5 {
		t.Fatalf("created %d areas, want exactly 5", okN)
	}
	_, err := s.CreateArea(ctx, a, hh, "More", 5)
	is(t, err, store.ErrLimit)
}

func TestCompletionHistoryRetention(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	tick := 0
	s := newStore(t).WithClock(func() time.Time { tick++; return base.Add(time.Duration(tick) * time.Minute) })
	a := mkUser(t, s, "a")
	hh := mkHousehold(t, s, a, "H")
	noErr(t, s.UpdateInstanceSettings(ctx, store.SettingsUpdate{Limits: map[string]int{string(limits.CompletionsPerChore): 3}}))
	ch := must(s.CreateChore(ctx, a, hh, choreIn("Dust")))
	var ids []string
	for i := 0; i < 6; i++ {
		c, _, err := s.CompleteChoreAuto(ctx, a, hh, ch.ID, fmt.Sprintf("n%d", i), "app", "")
		noErr(t, err)
		ids = append(ids, c.ID)
	}
	got := must(s.ListCompletions(ctx, a, hh, ch.ID, 100))
	if len(got) != 3 {
		t.Fatalf("kept %d completions, want 3", len(got))
	}
	for _, c := range got {
		if c.ID == ids[0] || c.ID == ids[1] || c.ID == ids[2] {
			t.Fatalf("an old completion survived: %s", c.Note)
		}
	}
}

func TestHouseholdsPerUserIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := mkUser(t, s, "a")
	var wg sync.WaitGroup
	var mu sync.Mutex
	okN := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateHousehold(ctx, a, store.NewHousehold{Name: fmt.Sprintf("H%d", i), Timezone: "UTC", MaxHouseholds: 3})
			if err == nil {
				mu.Lock()
				okN++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if hs := must(s.ListHouseholds(ctx, a)); okN != 3 || len(hs) != 3 {
		t.Fatalf("created %d, listed %d, want 3", okN, len(hs))
	}
	_, err := s.CreateHousehold(ctx, a, store.NewHousehold{Name: "X", Timezone: "UTC", MaxHouseholds: 3})
	is(t, err, store.ErrLimit)
}

func TestInviteUseIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	owner := mkUser(t, s, "owner")
	hh := mkHousehold(t, s, owner, "H")
	_, err := s.CreateInvite(ctx, owner, hh, store.NewInvite{Role: store.RoleMember, TokenHash: "th", ExpiresAt: time.Now().Add(time.Hour), MaxActive: 5, MaxUses: 2})
	noErr(t, err)
	var wg sync.WaitGroup
	var mu sync.Mutex
	okN := 0
	for i := 0; i < 8; i++ {
		u := mkUser(t, s, fmt.Sprintf("u%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.AcceptInvite(ctx, u.UserID, "th", store.JoinLimits{MaxMembers: 50, MaxHouseholds: 20}); err == nil {
				mu.Lock()
				okN++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okN != 2 || len(must(s.ListMembers(ctx, owner, hh))) != 3 {
		t.Fatalf("%d joins succeeded, want exactly 2", okN)
	}
}

// An invite link names its household and inviter only while it can be used.
func TestPreviewInvite(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	owner := mkUser(t, s, "owner")
	other := mkUser(t, s, "other")
	hh := mkHousehold(t, s, owner, "Maple Street 12")
	noErr(t, s.AddMember(ctx, owner, hh, other.UserID, store.RoleOwner))
	mk := func(a store.Actor, hash string) store.Invite {
		return must(s.CreateInvite(ctx, a, hh, store.NewInvite{Role: store.RoleMember, TokenHash: hash, ExpiresAt: time.Now().Add(time.Hour), MaxActive: 9}))
	}
	mk(owner, "live")
	p := must(s.PreviewInvite(ctx, "live"))
	if p.HouseholdName != "Maple Street 12" || p.InviterName != "owner" {
		t.Fatalf("preview = %+v", p)
	}
	_, err := s.PreviewInvite(ctx, "unknown")
	is(t, err, store.ErrNotFound)

	revoked := mk(owner, "revoked")
	noErr(t, s.RevokeInvite(ctx, owner, hh, revoked.ID))
	_, err = s.PreviewInvite(ctx, "revoked")
	is(t, err, store.ErrNotFound)

	mk(owner, "used")
	joiner := mkUser(t, s, "joiner")
	must(s.AcceptInvite(ctx, joiner.UserID, "used", store.JoinLimits{MaxMembers: 50, MaxHouseholds: 20}))
	_, err = s.PreviewInvite(ctx, "used")
	is(t, err, store.ErrNotFound)

	// The inviter's account is gone: the household still shows, the name does not.
	mk(other, "orphan")
	must(s.DeleteAccount(ctx, other.UserID, store.AccountDeletion{}))
	p = must(s.PreviewInvite(ctx, "orphan"))
	if p.HouseholdName != "Maple Street 12" || p.InviterName != "" {
		t.Fatalf("orphan preview = %+v", p)
	}
}

// Every count-then-insert limit holds under concurrent creates: the count is
// taken under the household or user lock.
func TestCountedCreatesAreAtomic(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := mkUser(t, s, "a")
	hh := mkHousehold(t, s, a, "H")
	client := must(s.CreateOAuthClient(ctx, "app", []string{"https://app.example/cb"}, 100, 100, ""))
	const max = 3
	creates := map[string]func(i int) error{
		"chores": func(i int) error {
			in := choreIn(fmt.Sprintf("C%d", i))
			in.MaxChores = max
			_, err := s.CreateChore(ctx, a, hh, in)
			return err
		},
		"household channels": func(int) error {
			_, err := s.CreateHouseholdChannel(ctx, a, hh, store.NewChannel{ID: ids.New(), Service: "discord", Ciphertext: "v1:x", Label: "L", Max: max})
			return err
		},
		"user channels": func(int) error {
			_, err := s.CreateUserChannel(ctx, a, store.NewChannel{ID: ids.New(), Service: "discord", Ciphertext: "v1:x", Label: "L", Max: max})
			return err
		},
		"webhooks": func(int) error {
			_, err := s.CreateWebhook(ctx, a, hh, store.NewWebhook{ID: ids.New(), URLCiphertext: "u", URLHint: "h", SecretCiphertext: "s",
				Events: store.WebhookEvents, Max: max})
			return err
		},
		"feeds": func(int) error {
			_, _, err := s.CreateFeed(ctx, a, hh, max)
			return err
		},
		"api tokens": func(int) error {
			_, err := s.CreateAPIToken(ctx, a, store.NewAPIToken{Name: "n", TokenHash: ids.Hash(ids.Token()),
				Scopes: []string{store.ScopeRead}, HouseholdIDs: []string{hh}, Max: max})
			return err
		},
		"oauth grants": func(int) error {
			_, err := s.CreateOAuthCode(ctx, a, store.NewOAuthCode{ClientID: client.ID, RedirectURI: "https://app.example/cb", Challenge: "c",
				CodeHash: ids.Hash(ids.Token()), Scopes: []string{store.ScopeRead}, HouseholdIDs: []string{hh}, Max: max})
			return err
		},
		"push subscriptions": func(i int) error {
			_, err := s.CreatePushSubscription(ctx, a, store.NewPushSubscription{ID: ids.New(), Endpoint: fmt.Sprintf("https://push.example/%d", i),
				Ciphertext: "v1:k", Max: max})
			return err
		},
		"passkeys": func(i int) error {
			_, _, err := s.AddPasskey(ctx, a.UserID, "", newKey("K", fmt.Sprintf("cred-%d", i)), max, nil)
			return err
		},
	}
	for name, create := range creates {
		t.Run(name, func(t *testing.T) {
			var wg sync.WaitGroup
			var mu sync.Mutex
			okN := 0
			for i := range 12 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					err := create(i)
					if err != nil && !errors.Is(err, store.ErrLimit) {
						t.Errorf("create %d: %v", i, err)
					}
					if err == nil {
						mu.Lock()
						okN++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			if okN != max {
				t.Fatalf("created %d, want exactly %d", okN, max)
			}
		})
	}
}
