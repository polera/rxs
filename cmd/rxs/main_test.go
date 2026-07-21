package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/store"
)

func TestAddSubcommandAddsAndRefreshesFeed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/rss+xml")
		_, _ = response.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0"><channel><title>Example Feed</title><link>https://example.test</link>
<item><guid>one</guid><title>First article</title><link>https://example.test/one</link></item>
</channel></rss>`))
	}))
	defer server.Close()

	dbPath := filepath.Join(t.TempDir(), "data", "rxs.db")
	var output bytes.Buffer
	if err := runArgs([]string{"add", "-db", dbPath, server.URL}, &output, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "Added Example Feed (1 new article(s))\n" {
		t.Fatalf("output = %q", got)
	}

	repository, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	feeds, err := repository.Feeds(context.Background())
	if err != nil || len(feeds) != 1 || feeds[0].Title != "Example Feed" {
		t.Fatalf("feeds = %#v, err = %v", feeds, err)
	}
	entries, err := repository.Entries(context.Background(), domain.EntryFilter{})
	if err != nil || len(entries) != 1 || entries[0].Title != "First article" {
		t.Fatalf("entries = %#v, err = %v", entries, err)
	}
}

func TestAddSubcommandRequiresExactlyOneURL(t *testing.T) {
	for _, args := range [][]string{{"add"}, {"add", "one", "two"}} {
		err := runArgs(args, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "rxs add") {
			t.Fatalf("runArgs(%q) error = %v", args, err)
		}
	}
}

func TestUnknownSubcommandIsRejected(t *testing.T) {
	err := runArgs([]string{"remove"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `unknown command "remove"`) {
		t.Fatalf("error = %v", err)
	}
}
