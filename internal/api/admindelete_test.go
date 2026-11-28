package api

import (
	"context"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// An admin deletes another account: only with a second factor and step-up,
// never their own or another admin's; ownership passes to the
// longest-standing member and lone households go with the account.
func TestAdminDeleteUser(t *testing.T) {
	f := newDelFixture(t) // ann owns Home (guest gu, then bob, then cy) and Solo
	e := f.e
	e.user("root", true)
	root2 := e.user("root2", true)
	ctx := context.Background()
	path := "/api/v1/admin/users/" + f.ann.ID

	// Non-admins get nothing.
	bob := e.signedInStepUp("bob")
	st, v := bob.do("GET", path+"/deletion-preview", nil)
	expect(t, st, v, 403, "forbidden")
	st, v = bob.do("DELETE", path, map[string]string{"confirmUsername": "ann"})
	expect(t, st, v, 403, "forbidden")

	// An admin without a second factor sees the preview but cannot delete.
	root := e.signedInStepUp("root")
	st, v = root.do("GET", path+"/deletion-preview", nil)
	expect(t, st, v, 200, "")
	if field(v, "canDelete") != false {
		t.Fatalf("canDelete without a second factor: %v", v)
	}
	del, _ := field(v, "deleteHouseholds").([]any)
	tr, _ := field(v, "transfers").([]any)
	if len(del) != 1 || field(del[0], "id") != f.solo || len(tr) != 1 || field(tr[0], "id") != f.home || field(tr[0], "newOwner") != f.bob.DisplayName {
		t.Fatalf("preview = %v", v)
	}
	st, v = root.do("DELETE", path, map[string]string{"confirmUsername": "ann"})
	expect(t, st, v, 403, "second_factor_required")

	// With an authenticator app: step-up is still needed after the change.
	st, v = root.do("POST", "/api/v1/account/totp/setup", nil)
	expect(t, st, v, 200, "")
	secret := field(v, "secret").(string)
	st, v = root.do("POST", "/api/v1/account/totp/confirm", map[string]string{"code": totpCode(secret, e.clock.Now())})
	expect(t, st, v, 200, "")
	e.clock.Add(31 * time.Second) // codes are single use; a later step-up needs a fresh one
	root = e.client()
	st, v = root.login("root", map[string]string{"totp": totpCode(secret, e.clock.Now())})
	expect(t, st, v, 200, "")
	st, v = root.do("DELETE", path, map[string]string{"confirmUsername": "ann"})
	expect(t, st, v, 403, "step_up_required")
	e.stepUp2(root, "root", secret)
	st, v = root.do("GET", path+"/deletion-preview", nil)
	if st != 200 || field(v, "canDelete") != true {
		t.Fatalf("preview with a second factor: %d %v", st, v)
	}

	// Refused: wrong username, own account, another admin, unknown account.
	st, v = root.do("DELETE", path, map[string]string{"confirmUsername": "bob"})
	expect(t, st, v, 400, "confirmation_mismatch")
	me, _ := e.st.UserByLogin(ctx, "root")
	st, v = root.do("DELETE", "/api/v1/admin/users/"+me.ID, map[string]string{"confirmUsername": "root"})
	expect(t, st, v, 403, "self_target")
	st, v = root.do("DELETE", "/api/v1/admin/users/"+root2.ID, map[string]string{"confirmUsername": "root2"})
	expect(t, st, v, 403, "admin_delete_target")
	st, v = root.do("GET", "/api/v1/admin/users/"+root2.ID+"/deletion-preview", nil)
	expect(t, st, v, 403, "admin_delete_target")
	st, v = root.do("DELETE", "/api/v1/admin/users/01a00000-0000-7000-8000-000000000000", map[string]string{"confirmUsername": "x"})
	expect(t, st, v, 404, "not_found")

	annC := e.signedIn("ann")
	st, v = root.do("DELETE", path, map[string]string{"confirmUsername": " ANN "})
	expect(t, st, v, 204, "")

	// Gone: sessions, the account, Solo; Home now belongs to bob (not the guest).
	if st, _ := annC.do("GET", "/api/v1/me", nil); st != 401 {
		t.Fatalf("deleted user's session still works: %d", st)
	}
	if _, err := e.st.UserByID(ctx, f.ann.ID); err == nil {
		t.Fatal("account still there")
	}
	st, v = bob.do("GET", "/api/v1/households/"+f.home+"/members", nil)
	expect(t, st, v, 200, "")
	for _, m := range v.([]any) {
		if field(m, "userId") == f.bob.ID && field(m, "role") != string(store.RoleOwner) {
			t.Fatalf("bob did not become owner: %v", v)
		}
	}
	if st, _ := e.signedIn("cy").do("GET", "/api/v1/households/"+f.solo+"/members", nil); st != 404 {
		t.Fatalf("Solo survived: %d", st)
	}
	// The admin's audit log records it.
	st, v = root.do("GET", "/api/v1/account/audit", nil)
	found := false
	for _, ev := range v.([]any) {
		if field(ev, "event") == store.AuditAdminUserDelete {
			found = true
		}
	}
	if st != 200 || !found {
		t.Fatalf("admin_user_delete missing: %d %v", st, v)
	}
}
