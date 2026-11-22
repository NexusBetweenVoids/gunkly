package db

import (
	"context"
	"slices"
	"testing"
)

func TestCopyOrderRespectsForeignKeys(t *testing.T) {
	ts := []table{
		{name: "c", deps: []string{"b", "a"}},
		{name: "b", deps: []string{"a"}},
		{name: "a"},
		{name: "self", deps: []string{"self"}},
	}
	got, err := copyOrder(ts)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tb := range got {
		names = append(names, tb.name)
	}
	if !slices.Equal(names, []string{"a", "self", "b", "c"}) {
		t.Fatalf("order %v", names)
	}
	if _, err := copyOrder([]table{{name: "x", deps: []string{"y"}}, {name: "y", deps: []string{"x"}}}); err == nil {
		t.Fatal("cycle accepted")
	}
	if _, err := copyOrder([]table{{name: "x", deps: []string{"gone"}}}); err == nil {
		t.Fatal("unknown reference accepted")
	}
}

func TestNormalise(t *testing.T) {
	cases := []struct {
		k    valueKind
		in   any
		want any
		ok   bool
	}{
		{kindBool, int64(1), true, true},
		{kindBool, int64(0), false, true},
		{kindBool, true, true, true},
		{kindBool, int64(2), nil, false},
		{kindInt, int64(7), int64(7), true},
		{kindInt, "42", int64(42), true},
		{kindInt, "x", nil, false},
		{kindText, []byte("hi"), "hi", true},
		{kindText, "hi", "hi", true},
		{kindText, int64(3), nil, false},
		{kindText, nil, nil, true},
	}
	for _, c := range cases {
		got, err := normalise(c.k, c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("normalise(%v, %v) = %v, %v", c.k, c.in, got, err)
		}
	}
}

func TestRowHashDistinguishesValues(t *testing.T) {
	sum := func(rows ...[]any) string {
		h := newRowHash()
		for _, r := range rows {
			h.add(r)
		}
		return h.report("t").Checksum
	}
	a := sum([]any{"ab", "c"})
	if a != sum([]any{"ab", "c"}) {
		t.Fatal("not deterministic")
	}
	for _, other := range [][]any{{"a", "bc"}, {"ab", nil}, {"ab", int64(0)}, {"ab", false}} {
		if sum(other) == a {
			t.Fatalf("collision with %v", other)
		}
	}
	if sum([]any{"x"}, []any{"y"}) == sum([]any{"y"}, []any{"x"}) {
		t.Fatal("row order ignored")
	}
}

// The source schema is read from SQLite itself: every table is found, and
// the derived order puts referenced tables first.
func TestSourceSchemaOfMigratedDatabase(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, "sqlite://"+t.TempDir()+"/x.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ts, err := sourceSchema(ctx, d.DB)
	if err != nil {
		t.Fatal(err)
	}
	order, err := copyOrder(ts)
	if err != nil || len(order) != len(ts) || len(ts) < 20 {
		t.Fatalf("%d tables, order %d, %v", len(ts), len(order), err)
	}
	pos := map[string]int{}
	for i, tb := range order {
		pos[tb.name] = i
	}
	for _, tb := range ts {
		for _, dep := range tb.deps {
			if dep != tb.name && pos[dep] > pos[tb.name] {
				t.Errorf("%s copied before %s", tb.name, dep)
			}
		}
		if len(tb.cols) == 0 {
			t.Errorf("%s has no columns", tb.name)
		}
	}
	if pos["users"] > pos["memberships"] || pos["chores"] > pos["completions"] {
		t.Fatal("expected parents first")
	}
}
