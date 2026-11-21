package db

import (
	"testing"
	"testing/fstest"
)

func TestLoadMigrationsDialectOverride(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_init.sql":          {Data: []byte("shared1")},
		"0001_init.postgres.sql": {Data: []byte("pg1")},
		"0002_more.sql":          {Data: []byte("shared2")},
		"0003_only.sqlite.sql":   {Data: []byte("lite3")},
		"README.md":              {Data: []byte("ignored")},
	}
	for d, want := range map[Dialect][]string{
		SQLite:   {"0001_init=shared1", "0002_more=shared2", "0003_only=lite3"},
		Postgres: {"0001_init=pg1", "0002_more=shared2"},
	} {
		ms, err := loadMigrations(fsys, d)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range ms {
			got = append(got, m.version+"="+m.sql)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: got %v want %v", d, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: got %v want %v", d, got, want)
			}
		}
	}
	if _, err := loadMigrations(fstest.MapFS{"0001_x.mysql.sql": {}}, SQLite); err == nil {
		t.Error("unknown dialect suffix accepted")
	}
}
