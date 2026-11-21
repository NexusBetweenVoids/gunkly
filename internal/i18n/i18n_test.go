package i18n

import "testing"

func TestT(t *testing.T) {
	for _, c := range []struct{ locale, key, want string }{
		{"en", "notify.on", "Dishes is due today."},
		{"en-GB", "notify.on", "Dishes is due today."},
		{"xx", "notify.on", "Dishes is due today."},
		{"", "notify.on", "Dishes is due today."},
		{"en", "no.such.key", "no.such.key"},
	} {
		if got := T(c.locale, c.key, map[string]string{"chore": "Dishes"}); got != c.want {
			t.Errorf("T(%q, %q) = %q, want %q", c.locale, c.key, got, c.want)
		}
	}
	// Values are not re-expanded.
	if got := T("en", "notify.on", map[string]string{"chore": "{chore}"}); got != "{chore} is due today." {
		t.Errorf("got %q", got)
	}
}

// Every catalog must have exactly the keys of the default one, with the same
// placeholders.
func TestCatalogsComplete(t *testing.T) {
	for l, m := range catalogs {
		for k := range catalogs[Default] {
			if _, ok := m[k]; !ok {
				t.Errorf("%s: missing %s", l, k)
			}
		}
		for k := range m {
			if _, ok := catalogs[Default][k]; !ok {
				t.Errorf("%s: unknown key %s", l, k)
			}
		}
	}
}
