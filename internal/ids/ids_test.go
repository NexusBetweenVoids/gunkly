package ids

import (
	"regexp"
	"testing"
)

var uuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNew(t *testing.T) {
	prev := ""
	for range 1000 {
		id := New()
		if !uuidV7.MatchString(id) {
			t.Fatalf("not a UUIDv7: %s", id)
		}
		if id == prev {
			t.Fatal("duplicate id")
		}
		prev = id
	}
}

func TestTokenHash(t *testing.T) {
	a, b := Token(), Token()
	if a == b || len(a) != 43 {
		t.Fatalf("bad tokens %q %q", a, b)
	}
	if !Equal(Hash(a), Hash(a)) || Equal(Hash(a), Hash(b)) {
		t.Fatal("hash compare broken")
	}
}
