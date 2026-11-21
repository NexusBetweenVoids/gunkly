package limits

import (
	"context"
	"errors"
	"testing"
)

type fakeSource struct{ inst, plan map[string]int }

func (f fakeSource) LimitOverrides(_ context.Context, hh string) (map[string]int, map[string]int, error) {
	if hh == "" {
		return f.inst, nil, nil
	}
	return f.inst, f.plan, nil
}

func TestLimitPrecedence(t *testing.T) {
	s := New(fakeSource{
		inst: map[string]int{string(MembersPerHousehold): 10, string(ChoresPerHousehold): 0},
		plan: map[string]int{string(MembersPerHousehold): 99},
	})
	ctx := context.Background()
	cases := []struct {
		k    Kind
		hh   string
		want int
	}{
		{MembersPerHousehold, "", 10},
		{MembersPerHousehold, "h", 99},
		{ChoresPerHousehold, "h", 2000}, // zero override ignored
		{ActiveInvitesPerHousehold, "h", 20},
	}
	for _, c := range cases {
		if got, err := s.Limit(ctx, c.k, c.hh); err != nil || got != c.want {
			t.Errorf("Limit(%s, %q) = %d, %v; want %d", c.k, c.hh, got, err, c.want)
		}
	}
	if err := s.Check(ctx, ActiveInvitesPerHousehold, "h", 19); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(ctx, ActiveInvitesPerHousehold, "h", 20); !errors.Is(err, ErrExceeded) {
		t.Fatal(err)
	}
	for k := range Defaults {
		if !Known(k) || Defaults[k] <= 0 {
			t.Errorf("bad default for %s", k)
		}
	}
	if Known("nope") {
		t.Fatal("unknown kind known")
	}
}
