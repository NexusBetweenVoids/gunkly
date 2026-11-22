package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

type bearerTransport struct{ secret string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.secret)
	return http.DefaultTransport.RoundTrip(r)
}

func mcpTestEnv(t *testing.T) *tokenEnv {
	f := newHH(t, func(o *Options) { o.Version = "9.9.9" })
	te := &tokenEnv{hhFixture: f}
	te.aStep, te.mStep, te.gStep = f.e.signedInStepUp("a"), f.e.signedInStepUp("m"), f.e.signedInStepUp("g")
	return te
}

// connect opens an MCP client session (in-process, over the test server).
func (f *tokenEnv) connect(t *testing.T, secret string) *mcp.ClientSession {
	t.Helper()
	cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := cl.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: f.e.ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{secret}}, DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tl := range res.Tools {
		out = append(out, tl.Name)
	}
	slices.Sort(out)
	return out
}

// call runs a tool and returns its decoded JSON text and whether it was a tool error
// (then the map holds the message under "error").
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v", name, err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("%s: %d content items", name, len(res.Content))
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		return map[string]any{"error": text}, true
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: not JSON: %q", name, text)
	}
	return out, false
}

func mustOK(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	out, isErr := callTool(t, cs, name, args)
	if isErr {
		t.Fatalf("%s %v: %v", name, args, out["error"])
	}
	return out
}

func mustFail(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	out, isErr := callTool(t, cs, name, args)
	if !isErr || !strings.Contains(strings.ToLower(out["error"].(string)), strings.ToLower(want)) {
		t.Fatalf("%s %v: got %v (error=%v), want an error containing %q", name, args, out, isErr, want)
	}
}

func TestMCPInitializeAndToolsPerScope(t *testing.T) {
	f := mcpTestEnv(t)
	readTools := []string{"get_chore", "list_chores", "list_households", "upcoming"}
	completeTools := []string{"complete_chore", "skip_chore", "snooze_chore", "undo_completion"}
	for _, c := range []struct {
		scopes []string
		want   []string
	}{
		{[]string{scRead}, readTools},
		{[]string{scComplete}, completeTools},
		{[]string{scWrite}, []string{"create_chore"}},
		{[]string{scRead, scComplete}, slices.Concat(readTools, completeTools)},
		{[]string{scRead, scComplete, scWrite}, slices.Concat(readTools, completeTools, []string{"create_chore"})},
	} {
		secret, _ := mkToken(t, f.aStep, c.scopes, f.hA)
		cs := f.connect(t, secret)
		if ir := cs.InitializeResult(); ir.ServerInfo.Name != "gunkly" || ir.ServerInfo.Version != "9.9.9" || ir.Instructions == "" {
			t.Errorf("server info %+v", ir)
		}
		got, want := toolNames(t, cs), slices.Sorted(slices.Values(c.want))
		if !slices.Equal(got, want) {
			t.Errorf("scopes %v: tools %v, want %v", c.scopes, got, want)
		}
	}
	// Every tool has a description and an input schema for the model.
	secret, _ := mkToken(t, f.aStep, []string{scRead, scComplete, scWrite}, f.hA)
	res, _ := f.connect(t, secret).ListTools(context.Background(), nil)
	for _, tl := range res.Tools {
		if len(tl.Description) < 40 || tl.InputSchema == nil {
			t.Errorf("tool %s is poorly described", tl.Name)
		}
	}
}

func TestMCPToolsHappyPath(t *testing.T) {
	f := mcpTestEnv(t)
	secret, tokID := mkToken(t, f.aStep, []string{scRead, scComplete, scWrite}, f.hA)
	cs := f.connect(t, secret)

	hs := mustOK(t, cs, "list_households", nil)["households"].([]any)
	if len(hs) != 1 || str(hs[0], "name") != "A" || str(hs[0], "role") != "owner" || str(hs[0], "id") != f.hA || str(hs[0], "timezone") != "Europe/Amsterdam" {
		t.Fatalf("households %v", hs)
	}

	// create_chore: interval, RRULE and one-off; assignee "me".
	ch := mustOK(t, cs, "create_chore", map[string]any{"name": "Water plants", "repeat": "every 2 weeks", "first_due": "2026-11-20", "assignees": []string{"me"}, "notes": "the big ones"})["chore"].(map[string]any)
	if ch["repeat"] != "every 2 weeks" || ch["status"] != "overdue" || ch["assignedToMe"] != true || ch["household"] != "A" || ch["notes"] != "the big ones" {
		t.Fatalf("created %v", ch)
	}
	plants := ch["id"].(string)
	mustOK(t, cs, "create_chore", map[string]any{"name": "Bins", "repeat": "FREQ=WEEKLY;BYDAY=MO", "first_due": "2026-11-21"})
	today := mustOK(t, cs, "create_chore", map[string]any{"name": "Call plumber"})["chore"].(map[string]any)
	if today["repeat"] != "once" || today["dueDate"] != "2026-11-21" || today["status"] != "today" {
		t.Fatalf("one-off %v", today)
	}
	mustOK(t, cs, "create_chore", map[string]any{"name": "Gutters", "repeat": "every year", "first_due": "2027-03-01"})
	// It is a normal chore in the REST API too.
	st, v := f.a.do("GET", f.chore(plants), nil)
	expect(t, st, v, 200, "")
	if field(field(v, "schedule"), "interval").(map[string]any)["every"] != float64(2) {
		t.Fatalf("rest view %v", v)
	}

	// list_chores: filters.
	all := mustOK(t, cs, "list_chores", nil)
	if all["total"] != float64(4) || all["truncated"] != false {
		t.Fatalf("all %v", all)
	}
	names := func(out map[string]any) []string {
		var ns []string
		for _, c := range out["chores"].([]any) {
			ns = append(ns, str(c, "name"))
		}
		return ns
	}
	if got := names(mustOK(t, cs, "list_chores", map[string]any{"status": "overdue"})); !slices.Equal(got, []string{"Water plants"}) {
		t.Fatalf("overdue %v", got)
	}
	if got := names(mustOK(t, cs, "list_chores", map[string]any{"status": "today", "household": "a"})); !slices.Equal(got, []string{"Call plumber"}) {
		t.Fatalf("today %v", got)
	}
	if got := names(mustOK(t, cs, "list_chores", map[string]any{"status": "later"})); !slices.Equal(got, []string{"Bins", "Gutters"}) {
		t.Fatalf("later %v", got)
	}
	mustFail(t, cs, "list_chores", map[string]any{"status": "someday"}, "status must be")
	mine := names(mustOK(t, cs, "list_chores", map[string]any{"mine": true}))
	if len(mine) != 4 { // assigned to me, or to nobody
		t.Fatalf("mine %v", mine)
	}

	// get_chore by exact name, case-insensitive; history empty at first.
	g := mustOK(t, cs, "get_chore", map[string]any{"chore": "water PLANTS"})
	if g["chore"].(map[string]any)["id"] != plants || len(g["recentCompletions"].([]any)) != 0 {
		t.Fatalf("get %v", g)
	}

	// complete_chore -> next due; recorded as the token.
	done := mustOK(t, cs, "complete_chore", map[string]any{"chore": plants, "note": "done by assistant"})
	compID := done["completionId"].(string)
	next := done["chore"].(map[string]any)
	if next["status"] == "overdue" || next["dueDate"] == "2026-11-20" {
		t.Fatalf("not advanced: %v", next)
	}
	st, v = f.a.do("GET", f.chore(plants)+"/completions", nil)
	expect(t, st, v, 200, "")
	items := field(v, "items").([]any)
	if len(items) != 1 || str(items[0], "source") != store.SourceAPIToken || str(items[0], "note") != "done by assistant" || str(items[0], "id") != compID {
		t.Fatalf("completion %v", items)
	}
	evs, _ := f.e.st.ListAuditForUser(context.Background(), f.userID("a"), 50)
	audited := false
	for _, ev := range evs {
		audited = audited || ev.Event == store.AuditTokenComplete && ev.Meta["token"] == tokID && ev.Meta["completion"] == compID
	}
	if !audited {
		t.Fatalf("no token_complete audit event for %s: %v", tokID, evs)
	}
	hist := mustOK(t, cs, "get_chore", map[string]any{"chore": plants})["recentCompletions"].([]any)
	if len(hist) != 1 || hist[0].(map[string]any)["via"] != "api_token" || hist[0].(map[string]any)["byTokenOwner"] != true {
		t.Fatalf("history %v", hist)
	}

	// undo_completion restores the due date.
	undone := mustOK(t, cs, "undo_completion", map[string]any{"completion": compID})["chore"].(map[string]any)
	if undone["dueDate"] != "2026-11-20" || undone["status"] != "overdue" {
		t.Fatalf("undo %v", undone)
	}
	mustFail(t, cs, "undo_completion", map[string]any{"completion": "nope"}, "no completion")

	// snooze_chore.
	sn := mustOK(t, cs, "snooze_chore", map[string]any{"chore": "Water plants", "until": "2026-11-25"})["chore"].(map[string]any)
	if sn["dueDate"] != "2026-11-25" {
		t.Fatalf("snooze %v", sn)
	}
	mustFail(t, cs, "snooze_chore", map[string]any{"chore": plants, "until": "2020-01-01"}, "today or later")
	mustFail(t, cs, "snooze_chore", map[string]any{"chore": plants, "until": "soon"}, "date")

	// skip_chore: recurring advances, one-off refused.
	bins := mustOK(t, cs, "skip_chore", map[string]any{"chore": "Bins"})["chore"].(map[string]any)
	if bins["dueDate"] != "2026-11-30" {
		t.Fatalf("skip %v", bins)
	}
	mustFail(t, cs, "skip_chore", map[string]any{"chore": "Call plumber"}, "one-off")

	// upcoming: dates ascending, overdue flagged, window respected.
	up := mustOK(t, cs, "upcoming", map[string]any{"days": 7})["upcoming"].([]any)
	var got []string
	for _, e := range up {
		got = append(got, str(e, "date")+" "+str(e, "chore"))
	}
	if !slices.Equal(got, []string{"2026-11-21 Call plumber", "2026-11-25 Water plants"}) {
		t.Fatalf("upcoming %v", got)
	}
	mustFail(t, cs, "upcoming", map[string]any{"days": 0}, "between 1 and 62")
	mustFail(t, cs, "upcoming", map[string]any{"days": 63}, "between 1 and 62")

	// Ambiguity, unknown chores and bad input are explained.
	mustOK(t, cs, "create_chore", map[string]any{"name": "bins"})
	mustFail(t, cs, "get_chore", map[string]any{"chore": "BINS"}, "several chores")
	mustFail(t, cs, "get_chore", map[string]any{"chore": "nothing"}, "no chore")
	mustFail(t, cs, "create_chore", map[string]any{"name": "x", "repeat": "whenever"}, "not understood")
	mustFail(t, cs, "create_chore", map[string]any{"name": "", "repeat": "daily"}, "invalid chore")
	mustFail(t, cs, "create_chore", map[string]any{"name": "x", "assignees": []string{"someone-else"}}, "")
}

func TestMCPScopeRefusals(t *testing.T) {
	f := mcpTestEnv(t)
	chore := f.mk(t, f.a, intervalBody("Dishes", "2026-11-21"))
	read, _ := mkToken(t, f.aStep, []string{scRead}, f.hA)
	cs := f.connect(t, read)
	// Not listed, yet a call explains which permission is missing.
	mustFail(t, cs, "complete_chore", map[string]any{"chore": chore}, scComplete)
	mustFail(t, cs, "undo_completion", map[string]any{"completion": "x"}, scComplete)
	mustFail(t, cs, "snooze_chore", map[string]any{"chore": chore, "until": "2026-12-01"}, scComplete)
	mustFail(t, cs, "skip_chore", map[string]any{"chore": chore}, scComplete)
	mustFail(t, cs, "create_chore", map[string]any{"name": "Nope"}, scWrite)
	mustOK(t, cs, "list_chores", nil)

	write, _ := mkToken(t, f.aStep, []string{scWrite}, f.hA)
	cw := f.connect(t, write)
	mustFail(t, cw, "list_chores", nil, scRead)
	mustFail(t, cw, "list_households", nil, scRead)
	mustFail(t, cw, "complete_chore", map[string]any{"chore": chore}, scComplete)
	mustOK(t, cw, "create_chore", map[string]any{"name": "Ok"})

	// Nothing was changed by the refused calls.
	st, v := f.a.do("GET", f.base()+"/chores", nil)
	expect(t, st, v, 200, "")
	if len(list(t, v)) != 2 {
		t.Fatalf("chores %v", v)
	}
	st, v = f.a.do("GET", f.chore(chore)+"/completions", nil)
	if expect(t, st, v, 200, ""); len(field(v, "items").([]any)) != 0 {
		t.Fatalf("completion recorded %v", v)
	}
}

func TestMCPHouseholdIsolation(t *testing.T) {
	f := mcpTestEnv(t)
	ctx := context.Background()
	// a also belongs to B, so only the token binding keeps it out.
	if err := f.e.st.AddMember(ctx, store.Actor{UserID: f.userID("b")}, f.hB, f.userID("a"), store.RoleMember); err != nil {
		t.Fatal(err)
	}
	cB := mkIn(t, f.b, f.hB, intervalBody("B secret chore", "2026-11-21"))
	cA := f.mk(t, f.a, intervalBody("A chore", "2026-11-21"))
	st, v := f.b.do("POST", "/api/v1/households/"+f.hB+"/chores/"+cB+"/complete", nil)
	expect(t, st, v, 201, "")
	compB := str(field(v, "completion"), "id")

	secret, _ := mkToken(t, f.aStep, []string{scRead, scComplete, scWrite}, f.hA)
	cs := f.connect(t, secret)
	for _, hh := range []string{f.hB, "B", "b"} {
		mustFail(t, cs, "list_chores", map[string]any{"household": hh}, "available to this token")
		mustFail(t, cs, "get_chore", map[string]any{"household": hh, "chore": cB}, "available to this token")
		mustFail(t, cs, "complete_chore", map[string]any{"household": hh, "chore": cB}, "available to this token")
		mustFail(t, cs, "create_chore", map[string]any{"household": hh, "name": "Sneaky"}, "available to this token")
		mustFail(t, cs, "upcoming", map[string]any{"household": hh, "days": 7}, "available to this token")
		mustFail(t, cs, "undo_completion", map[string]any{"household": hh, "completion": compB}, "available to this token")
	}
	// B's chore ids and names do not resolve inside A.
	mustFail(t, cs, "get_chore", map[string]any{"chore": cB}, "no chore")
	mustFail(t, cs, "complete_chore", map[string]any{"chore": "B secret chore"}, "no chore")
	mustFail(t, cs, "skip_chore", map[string]any{"chore": cB}, "no chore")
	mustFail(t, cs, "snooze_chore", map[string]any{"chore": cB, "until": "2026-12-01"}, "no chore")
	mustFail(t, cs, "undo_completion", map[string]any{"completion": compB}, "no completion")
	// Nothing leaked from the listings.
	if hs := mustOK(t, cs, "list_households", nil)["households"].([]any); len(hs) != 1 || str(hs[0], "name") != "A" {
		t.Fatalf("households %v", hs)
	}
	out := mustOK(t, cs, "list_chores", nil)
	if b, _ := json.Marshal(out); strings.Contains(string(b), "B secret") || !strings.Contains(string(b), cA) {
		t.Fatalf("list %s", b)
	}
	if b, _ := json.Marshal(mustOK(t, cs, "upcoming", map[string]any{"days": 30})); strings.Contains(string(b), "B secret") {
		t.Fatalf("upcoming leaked %s", b)
	}
	// B's chore is untouched.
	st, v = f.b.do("GET", "/api/v1/households/"+f.hB+"/chores/"+cB+"/completions", nil)
	if expect(t, st, v, 200, ""); len(field(v, "items").([]any)) != 1 {
		t.Fatalf("B history changed: %v", v)
	}

	// Leaving the household cuts the token off, like any outsider.
	if err := f.e.st.AddMember(ctx, store.Actor{UserID: f.userID("a")}, f.hA, f.userID("x"), store.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := f.e.st.RemoveMember(ctx, store.Actor{UserID: f.userID("a")}, f.hA, f.userID("a")); err != nil {
		t.Fatal(err)
	}
	mustFail(t, cs, "list_chores", nil, "no household")
	mustFail(t, cs, "complete_chore", map[string]any{"chore": cA}, "no household")
	if hs := mustOK(t, cs, "list_households", nil)["households"].([]any); len(hs) != 0 {
		t.Fatalf("left household still listed: %v", hs)
	}
}

func TestMCPSeveralHouseholds(t *testing.T) {
	f := mcpTestEnv(t)
	st, v := f.a.do("POST", "/api/v1/households", map[string]string{"name": "Cabin", "timezone": "Europe/Amsterdam"})
	expect(t, st, v, 201, "")
	hC := str(v, "id")
	cA := f.mk(t, f.a, intervalBody("Sweep", "2026-11-21"))
	cC := mkIn(t, f.a, hC, intervalBody("Sweep", "2026-11-22"))
	secret, _ := mkToken(t, f.aStep, []string{scRead, scComplete, scWrite}, f.hA, hC)
	cs := f.connect(t, secret)

	if hs := mustOK(t, cs, "list_households", nil)["households"].([]any); len(hs) != 2 {
		t.Fatalf("households %v", hs)
	}
	if out := mustOK(t, cs, "list_chores", nil); out["total"] != float64(2) {
		t.Fatalf("aggregated %v", out)
	}
	if out := mustOK(t, cs, "list_chores", map[string]any{"household": "cabin"}); out["total"] != float64(1) {
		t.Fatalf("cabin %v", out)
	}
	// Writes and lookups need the household when there are several.
	mustFail(t, cs, "get_chore", map[string]any{"chore": cA}, "household is required")
	mustFail(t, cs, "create_chore", map[string]any{"name": "x"}, "household is required")
	g := mustOK(t, cs, "get_chore", map[string]any{"household": "Cabin", "chore": "sweep"})
	if g["chore"].(map[string]any)["id"] != cC {
		t.Fatalf("got %v", g)
	}
	// An id from the other household does not resolve.
	mustFail(t, cs, "complete_chore", map[string]any{"household": "Cabin", "chore": cA}, "no chore")
	if out := mustOK(t, cs, "upcoming", map[string]any{"days": 5}); len(out["upcoming"].([]any)) != 2 {
		t.Fatalf("upcoming %v", out)
	}
}

func TestMCPGuestTokenIsReadOnly(t *testing.T) {
	f := mcpTestEnv(t)
	chore := f.mk(t, f.a, intervalBody("Dishes", "2026-11-21"))
	secret, _ := mkToken(t, f.gStep, []string{scRead, scComplete, scWrite}, f.hA)
	cs := f.connect(t, secret)
	mustOK(t, cs, "list_chores", nil)
	mustOK(t, cs, "get_chore", map[string]any{"chore": "Dishes"})
	mustOK(t, cs, "upcoming", map[string]any{"days": 3})
	if hs := mustOK(t, cs, "list_households", nil)["households"].([]any); str(hs[0], "role") != "guest" {
		t.Fatalf("role %v", hs)
	}
	mustFail(t, cs, "complete_chore", map[string]any{"chore": chore}, "guest")
	mustFail(t, cs, "snooze_chore", map[string]any{"chore": chore, "until": "2026-12-01"}, "guest")
	mustFail(t, cs, "skip_chore", map[string]any{"chore": chore}, "guest")
	mustFail(t, cs, "undo_completion", map[string]any{"completion": "x"}, "guest")
	mustFail(t, cs, "create_chore", map[string]any{"name": "x"}, "guest")
	st, v := f.a.do("GET", f.chore(chore)+"/completions", nil)
	if expect(t, st, v, 200, ""); len(field(v, "items").([]any)) != 0 {
		t.Fatalf("guest completed: %v", v)
	}
	// A demotion applies to an existing token at once.
	msecret, _ := mkToken(t, f.mStep, []string{scComplete}, f.hA)
	cm := f.connect(t, msecret)
	mustOK(t, cm, "complete_chore", map[string]any{"chore": chore})
	if err := f.e.st.ChangeRole(context.Background(), store.Actor{UserID: f.userID("a")}, f.hA, f.mID, store.RoleGuest); err != nil {
		t.Fatal(err)
	}
	mustFail(t, cm, "complete_chore", map[string]any{"chore": chore}, "guest")
}

// rawMCP posts a JSON-RPC message to /mcp with the given headers.
func (e *env) rawMCP(method string, secret string, body string, hdr ...string) (int, http.Header, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+"/mcp", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp.StatusCode, resp.Header, b.String()
}

const listToolsRPC = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

func TestMCPAuthOriginAndTransport(t *testing.T) {
	f := mcpTestEnv(t)
	e := f.e
	secret, tokID := mkToken(t, f.aStep, []string{scRead}, f.hA)

	// No credential, a malformed one, an unknown one: 401 with the challenge, for every method.
	for _, m := range []string{"POST", "GET", "DELETE"} {
		for _, s := range []string{"", "gk_" + strings.Repeat("x", 43), "short"} {
			st, hdr, _ := e.rawMCP(m, s, listToolsRPC)
			if st != 401 || hdr.Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("%s with token %q: %d %q", m, s, st, hdr.Get("WWW-Authenticate"))
			}
		}
	}
	// A session cookie is no credential here, and needs no CSRF token to be refused as unauthenticated.
	req, _ := http.NewRequest("POST", e.ts.URL+"/mcp", strings.NewReader(listToolsRPC))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := f.a.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("cookie session: %d", resp.StatusCode)
	}
	if st, _, body := e.rawMCP("POST", secret, listToolsRPC); st != 200 || !strings.Contains(body, "list_chores") {
		t.Fatalf("valid token: %d %s", st, body)
	}
	// Stateless: only POST; no session header is handed out.
	if st, hdr, _ := e.rawMCP("GET", secret, ""); st != 405 || hdr.Get("Mcp-Session-Id") != "" {
		t.Errorf("GET: %d", st)
	}
	if st, _, _ := e.rawMCP("DELETE", secret, ""); st != 405 {
		t.Errorf("DELETE: %d", st)
	}
	// Body cap like the other API routes.
	if st, _, _ := e.rawMCP("POST", secret, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"pad":"`+strings.Repeat("a", 2<<20)+`"}}`); st != 413 {
		t.Errorf("oversized body: %d", st)
	}
	// Use counts as token use (last used is touched).
	toks, _ := e.st.ListAPITokens(context.Background(), f.userID("a"))
	for _, tk := range toks {
		if tk.ID == tokID && tk.LastUsedAt.IsZero() {
			t.Error("last_used_at not written")
		}
	}

	// Origin (MCP spec DNS-rebinding guidance): a browser origin other than this instance is refused,
	// even with a valid token; the instance's own origin and no Origin pass.
	for _, o := range []string{"https://evil.example", "http://localhost:3000", "null"} {
		if st, _, _ := e.rawMCP("POST", secret, listToolsRPC, "Origin", o); st != 403 {
			t.Errorf("Origin %q: %d", o, st)
		}
	}
	if st, _, _ := e.rawMCP("POST", secret, listToolsRPC, "Origin", e.ts.URL); st != 200 {
		t.Errorf("own Origin: %d", st)
	}
	// With GUNKLY_BASE_URL set it is the reference, not the Host header.
	hb := newEnv(t, func(o *Options) { o.BaseURL = "https://gunkly.example" })
	if st, _, _ := hb.rawMCP("POST", "", listToolsRPC, "Origin", hb.ts.URL); st != 403 {
		t.Errorf("Origin = Host but not BASE_URL: %d", st)
	}
	if st, _, _ := hb.rawMCP("POST", "", listToolsRPC, "Origin", "https://gunkly.example"); st != 401 {
		t.Errorf("Origin = BASE_URL passes the origin check: %d", st)
	}
	// Per-token rate limit applies (the real limiter).
	rl := newTokenEnvLimited(t)
	var last int
	for range 200 {
		if last, _, _ = rl.e.rawMCP("POST", rl.secret, listToolsRPC); last == 429 {
			break
		}
	}
	if last != 429 {
		t.Errorf("no rate limit: %d", last)
	}
}

type limitedEnv struct {
	e      *env
	secret string
}

func newTokenEnvLimited(t *testing.T) limitedEnv {
	f := mcpTestEnv(t)
	secret, _ := mkToken(t, f.aStep, []string{scRead}, f.hA)
	f.e.srv.rl = newRateLimiter(10, 10, f.e.clock.Now)
	f.e.srv.rl.perMinute, f.e.srv.rl.burstOf = bucketRates, bucketBursts
	return limitedEnv{f.e, secret}
}

func TestMCPCanBeSwitchedOff(t *testing.T) {
	f := mcpTestEnv(t)
	secret, _ := mkToken(t, f.aStep, []string{scRead}, f.hA)
	f.e.user("root", true)
	admin := f.e.signedInStepUp("root")
	st, v := admin.do("GET", "/api/v1/admin/settings", nil)
	expect(t, st, v, 200, "")
	if field(v, "mcpEnabled") != true {
		t.Fatalf("default: %v", v)
	}
	if st, _, _ := f.e.rawMCP("POST", secret, listToolsRPC); st != 200 {
		t.Fatalf("on: %d", st)
	}
	st, v = admin.do("PATCH", "/api/v1/admin/settings", map[string]any{"mcpEnabled": false})
	expect(t, st, v, 200, "")
	if field(v, "mcpEnabled") != false {
		t.Fatalf("patched: %v", v)
	}
	// Gone for everyone, authenticated or not, on every method.
	for _, m := range []string{"POST", "GET", "DELETE"} {
		for _, s := range []string{secret, ""} {
			if st, _, _ := f.e.rawMCP(m, s, listToolsRPC); st != 404 {
				t.Errorf("%s off: %d", m, st)
			}
		}
	}
	st, v = admin.do("PATCH", "/api/v1/admin/settings", map[string]any{"mcpEnabled": true})
	expect(t, st, v, 200, "")
	if st, _, _ := f.e.rawMCP("POST", secret, listToolsRPC); st != 200 {
		t.Fatalf("on again: %d", st)
	}
	// Tokens never reach the admin settings.
	st, _, v = f.e.bearerDo("GET", "/api/v1/admin/settings", secret, nil)
	expect(t, st, v, 403, "token_not_allowed")
}

func TestParseRepeat(t *testing.T) {
	for in, want := range map[string]string{
		"every 2 weeks": "interval 2 week", "Every Day": "interval 1 day", "daily": "interval 1 day", "weekly": "interval 1 week",
		"monthly": "interval 1 month", "every 3 months": "interval 3 month", "every other week": "interval 2 week",
		"every year": "interval 12 month", "every 2 years": "interval 24 month", "week": "interval 1 week",
		"": "oneoff", "once": "oneoff", "FREQ=WEEKLY;BYDAY=MO,TH": "rrule FREQ=WEEKLY;BYDAY=MO,TH",
		"RRULE:FREQ=MONTHLY;BYMONTHDAY=1": "rrule FREQ=MONTHLY;BYMONTHDAY=1", "freq=daily": "rrule FREQ=DAILY",
	} {
		kind, sc, err := parseRepeat(in)
		got := kind
		switch {
		case err != nil:
			got = err.Error()
		case sc.Interval != nil:
			got = "interval " + itoa(sc.Interval.Every) + " " + sc.Interval.Unit
		case sc.RRule != "":
			got = "rrule " + sc.RRule
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"whenever", "every 0 days", "every 5000 weeks", "every fortnight", "twice a day"} {
		if _, _, err := parseRepeat(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
