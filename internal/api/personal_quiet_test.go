package api

import "testing"

func TestPersonalQuietHours(t *testing.T) {
	f := newTokenEnv(t)
	st, v := f.m.do("GET", "/api/v1/me", nil)
	expect(t, st, v, 200, "")
	if field(v, "quietHours") != nil {
		t.Fatalf("default quietHours: %v", v)
	}
	// Overnight windows are fine; start == end and out-of-range values are not.
	st, v = f.m.do("PATCH", "/api/v1/me", map[string]any{"quietHours": map[string]int{"start": 1320, "end": 420}})
	expect(t, st, v, 200, "")
	q := field(v, "quietHours")
	if field(q, "start") != float64(1320) || field(q, "end") != float64(420) {
		t.Fatalf("quietHours: %v", v)
	}
	for _, bad := range []map[string]int{{"start": 60, "end": 60}, {"start": -1, "end": 60}, {"start": 0, "end": 1440}} {
		st, v = f.m.do("PATCH", "/api/v1/me", map[string]any{"quietHours": bad})
		expect(t, st, v, 400, "")
	}
	// Other users and a PATCH without the field are unaffected.
	st, v = f.a.do("GET", "/api/v1/me", nil)
	expect(t, st, v, 200, "")
	if field(v, "quietHours") != nil {
		t.Fatalf("leaked to another user: %v", v)
	}
	st, v = f.m.do("PATCH", "/api/v1/me", map[string]any{"locale": "nl"})
	expect(t, st, v, 200, "")
	if field(v, "quietHours") == nil {
		t.Fatalf("lost by an unrelated PATCH: %v", v)
	}
	// The data export carries it.
	st, v = f.mStep.do("GET", "/api/v1/account/export", nil)
	expect(t, st, v, 200, "")
	if p := field(field(v, "preferences"), "quietHours"); field(p, "start") != float64(1320) {
		t.Fatalf("export: %v", v)
	}
	// Session only: a token cannot read or change it.
	secret, _ := mkToken(t, f.mStep, []string{scRead, scWrite}, f.hA)
	st, _, v = f.e.bearerDo("PATCH", "/api/v1/me", secret, map[string]any{"quietHours": nil})
	expect(t, st, v, 403, "token_not_allowed")
	st, v = f.m.do("PATCH", "/api/v1/me", map[string]any{"quietHours": nil})
	expect(t, st, v, 200, "")
	if field(v, "quietHours") != nil {
		t.Fatalf("not cleared: %v", v)
	}
}

func TestPersonalTimeZone(t *testing.T) {
	f := newTokenEnv(t)
	st, v := f.m.do("GET", "/api/v1/me", nil)
	expect(t, st, v, 200, "")
	if field(v, "timeZone") != nil {
		t.Fatalf("default timeZone: %v", v)
	}
	for _, bad := range []string{"Mars/Olympus", "../etc/passwd", "Local-ish zone", "europe/nowhere"} {
		st, v = f.m.do("PATCH", "/api/v1/me", map[string]any{"timeZone": bad})
		expect(t, st, v, 400, "")
	}
	st, v = f.m.do("PATCH", "/api/v1/me", map[string]any{"timeZone": "Europe/Amsterdam"})
	expect(t, st, v, 200, "")
	if field(v, "timeZone") != "Europe/Amsterdam" {
		t.Fatalf("timeZone: %v", v)
	}
	// Unrelated PATCHes keep it; other users do not see it.
	st, v = f.m.do("PATCH", "/api/v1/me", map[string]any{"locale": "nl"})
	expect(t, st, v, 200, "")
	if field(v, "timeZone") != "Europe/Amsterdam" {
		t.Fatalf("lost by an unrelated PATCH: %v", v)
	}
	st, v = f.a.do("GET", "/api/v1/me", nil)
	expect(t, st, v, 200, "")
	if field(v, "timeZone") != nil {
		t.Fatalf("leaked to another user: %v", v)
	}
	st, v = f.mStep.do("GET", "/api/v1/account/export", nil)
	expect(t, st, v, 200, "")
	if field(field(v, "preferences"), "timeZone") != "Europe/Amsterdam" {
		t.Fatalf("export: %v", v)
	}
	secret, _ := mkToken(t, f.mStep, []string{scRead, scWrite}, f.hA)
	st, _, v = f.e.bearerDo("PATCH", "/api/v1/me", secret, map[string]any{"timeZone": nil})
	expect(t, st, v, 403, "token_not_allowed")
	// Empty and null both clear it.
	st, v = f.m.do("PATCH", "/api/v1/me", map[string]any{"timeZone": ""})
	expect(t, st, v, 200, "")
	if field(v, "timeZone") != nil {
		t.Fatalf("not cleared by empty: %v", v)
	}
	f.m.do("PATCH", "/api/v1/me", map[string]any{"timeZone": "Asia/Tokyo"})
	st, v = f.m.do("PATCH", "/api/v1/me", map[string]any{"timeZone": nil})
	expect(t, st, v, 200, "")
	if field(v, "timeZone") != nil {
		t.Fatalf("not cleared by null: %v", v)
	}
}
