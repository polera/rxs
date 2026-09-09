package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestAddSubcommandLoadsContentConfiguration(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	dbPath := filepath.Join(dir, "data", "rxs.db")
	if err := os.WriteFile(configPath, []byte(`{"content":{"full_articles":"always"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runArgs([]string{"add", "-config", configPath, "-db", dbPath, "https://example.test/feed"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "full_articles") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Dir(dbPath)); !os.IsNotExist(statErr) {
		t.Fatalf("database directory was created before config validation: %v", statErr)
	}
}

func TestUnknownSubcommandIsRejected(t *testing.T) {
	err := runArgs([]string{"remove"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `unknown command "remove"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestUpgradeSubcommandRejectsArguments(t *testing.T) {
	err := runArgs([]string{"upgrade", "later"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "does not accept arguments") {
		t.Fatalf("error = %v", err)
	}
}

func TestInstalledVersionPrefersLinkedRelease(t *testing.T) {
	tests := []struct {
		linked string
		module string
		want   string
	}{
		{linked: "v1.2.3", module: "v1.2.2", want: "v1.2.3"},
		{linked: "dev", module: "v1.2.2", want: "v1.2.2"},
		{linked: "dev", module: "(devel)", want: "dev"},
	}
	for _, test := range tests {
		if got := chooseInstalledVersion(test.linked, test.module); got != test.want {
			t.Fatalf("chooseInstalledVersion(%q, %q) = %q, want %q", test.linked, test.module, got, test.want)
		}
	}
}

func TestInvalidColorSchemeIsRejectedBeforeDatabaseOpen(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	dbPath := filepath.Join(dir, "data", "rxs.db")
	if err := os.WriteFile(configPath, []byte(`{"appearance":{"color_scheme":"midnight"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runArgs([]string{"-config", configPath, "-db", dbPath}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown color scheme") || !strings.Contains(err.Error(), "solarized-light") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Dir(dbPath)); !os.IsNotExist(statErr) {
		t.Fatalf("database directory was created before scheme validation: %v", statErr)
	}
}

type cancelTransport struct {
	started chan context.Context
	exited  chan struct{}
}

func (transport cancelTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.started <- request.Context()
	<-request.Context().Done()
	close(transport.exited)
	return nil, request.Context().Err()
}

func TestCLICommandsCancelActiveRequests(t *testing.T) {
	for _, command := range []string{"add", "upgrade"} {
		t.Run(command, func(t *testing.T) {
			// Both clients intentionally use the standard default transport. Replace
			// it only in this nonparallel test, without altering production factories.
			transport := cancelTransport{make(chan context.Context, 1), make(chan struct{})}
			previous := http.DefaultTransport
			http.DefaultTransport = transport
			t.Cleanup(func() { http.DefaultTransport = previous })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "rxs.db")
			var output bytes.Buffer
			done := make(chan error, 1)
			go func() {
				if command == "add" {
					done <- runAddCommand(ctx, []string{"https://example.test/rss"}, dbPath, filepath.Join(dir, "config.json"), &output, &bytes.Buffer{})
				} else {
					done <- runUpgradeCommand(ctx, nil, "v1.0.0", &output, &bytes.Buffer{})
				}
			}()
			select {
			case requestCtx := <-transport.started:
				if requestCtx.Err() != nil {
					t.Fatal("request started canceled")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("command did not start HTTP request")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled %s error = %v", command, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("command did not finish on cancellation")
			}
			select {
			case <-transport.exited:
			default:
				t.Fatal("command returned before request exited")
			}
			if output.Len() != 0 {
				t.Fatalf("canceled command reported success: %q", output.String())
			}
			if command == "add" {
				repository, err := store.Open(dbPath)
				if err != nil {
					t.Fatal(err)
				}
				defer repository.Close()
				feeds, err := repository.Feeds(context.Background())
				if err != nil || len(feeds) != 1 || feeds[0].LastError != "" {
					t.Fatalf("canceled refresh lost added feed or saved a spurious failure: feeds=%#v err=%v", feeds, err)
				}
			}
		})
	}
}
