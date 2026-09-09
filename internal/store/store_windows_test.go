package store

import (
	"net/url"
	"testing"
)

func TestDatabaseDSNWindowsPaths(t *testing.T) {
	dsn, err := databaseDSN(`C:\rxs data\literal%2F#name.db`)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "file" || u.Host != "" || u.Path != "/C:/rxs data/literal%2F#name.db" || u.Fragment != "" {
		t.Fatalf("drive URI=%q parsed=%#v err=%v", dsn, u, err)
	}
	for _, path := range []string{`\\server\share\rxs.db`, `//server/share/rxs.db`, `\\?\C:\rxs.db`, `\\.\C:\rxs.db`} {
		if _, err := databaseDSN(path); err == nil {
			t.Errorf("databaseDSN(%q) should reject UNC/device path", path)
		}
	}
}
