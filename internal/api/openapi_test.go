package api

import (
	"bufio"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	specPathRE   = regexp.MustCompile(`^  (/\S*):\s*$`)
	specMethodRE = regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
)

// specOperations lists "METHOD /path" for every operation under paths: in
// api/openapi.yaml (paths are relative to the /api/v1 server URL).
func specOperations(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ops := map[string]bool{}
	inPaths, path := false, ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, " ") && line != "" {
			inPaths = line == "paths:"
		}
		if !inPaths {
			continue
		}
		if m := specPathRE.FindStringSubmatch(line); m != nil {
			path = m[1]
		} else if m := specMethodRE.FindStringSubmatch(line); m != nil && path != "" {
			ops[strings.ToUpper(m[1])+" "+path] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return ops
}

// TestOpenAPICoversRoutes walks the route table: every /api/v1 route must be
// documented, and every documented operation must exist, so the spec cannot drift.
func TestOpenAPICoversRoutes(t *testing.T) {
	e := newEnv(t)
	spec := specOperations(t)
	registered := map[string]bool{}
	for _, r := range e.srv.routes {
		method, p, _ := strings.Cut(r.pattern, " ")
		rel, ok := strings.CutPrefix(p, "/api/v1")
		if !ok {
			continue // probes and static files
		}
		op := method + " " + rel
		registered[op] = true
		if !spec[op] {
			t.Errorf("route %q is not documented in api/openapi.yaml", r.pattern)
		}
	}
	var stale []string
	for op := range spec {
		if !registered[op] {
			stale = append(stale, op)
		}
	}
	sort.Strings(stale)
	for _, op := range stale {
		t.Errorf("api/openapi.yaml documents %q, which is not a registered route", op)
	}
	if len(spec) < 50 {
		t.Fatalf("parsed only %d operations", len(spec))
	}
}

var specSecurityRE = regexp.MustCompile(`^      security: .*bearerAuth: \[([a-z:]*)\]`)

// TestOpenAPIDocumentsTokenScopes: the operations that list bearerAuth are
// exactly the routes that accept API tokens, with the same scope.
func TestOpenAPIDocumentsTokenScopes(t *testing.T) {
	f, err := os.Open("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, inPaths, path, method := map[string]string{}, false, "", ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, " ") && line != "" {
			inPaths = line == "paths:"
		}
		if m := specPathRE.FindStringSubmatch(line); inPaths && m != nil {
			path = m[1]
		} else if m := specMethodRE.FindStringSubmatch(line); inPaths && m != nil {
			method = strings.ToUpper(m[1])
		} else if m := specSecurityRE.FindStringSubmatch(line); inPaths && m != nil {
			got[method+" /api/v1"+path] = m[1]
		}
	}
	want := map[string]string{"GET /api/v1/token": ""}
	for p, sc := range tokenRoutes {
		want[p] = sc
	}
	for p, sc := range want {
		if g, ok := got[p]; !ok || g != sc {
			t.Errorf("%s: spec scope %q (documented %v), want %q", p, g, ok, sc)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("%s lists bearerAuth but accepts no token", p)
		}
	}
}
