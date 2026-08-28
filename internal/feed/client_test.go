package feed

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polera/rxs/internal/domain"
)

func TestFetchFixtures(t *testing.T) {
	for _, fixture := range []string{"rss.xml", "atom.xml", "feed.json"} {
		t.Run(fixture, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "..", "testdata", "feeds", fixture))
			if err != nil {
				t.Fatal(err)
			}
			client := NewClient("test")
			client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return response(http.StatusOK, body, http.Header{"Etag": []string{`"fixture"`}}), nil
			})
			parsed, err := client.Fetch(context.Background(), domain.Feed{URL: "https://example.test/feed"})
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Title == "" || parsed.ETag != `"fixture"` || len(parsed.Entries) != 1 {
				t.Fatalf("unexpected parsed feed: %#v", parsed)
			}
			if parsed.Entries[0].Identity == "" || strings.Contains(parsed.Entries[0].Text, "<p>") {
				t.Fatalf("entry was not normalized: %#v", parsed.Entries[0])
			}
		})
	}
}

func TestFetchPreservesPlaintextAdvisoryFormatting(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>BSDSec</title>
  <id>https://example.test/feed</id>
  <updated>2026-08-25T20:12:04Z</updated>
  <entry>
    <title>Security advisory</title>
    <id>advisory-1</id>
    <updated>2026-08-25T20:12:04Z</updated>
    <content type="html">Branch/path      Hash
- ------------------------
See &lt;URL:https://security.example/&gt;.
# git show &lt;commit hash&gt;</content>
  </entry>
</feed>`)
	client := NewClient("test")
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, body, nil), nil
	})

	parsed, err := client.Fetch(context.Background(), domain.Feed{URL: "https://example.test/feed"})
	if err != nil {
		t.Fatal(err)
	}
	want := "    Branch/path      Hash\n    - ------------------------\n    See <URL:https://security.example/>.\n    # git show <commit hash>"
	if got := parsed.Entries[0].Text; got != want {
		t.Fatalf("entry text = %q, want %q", got, want)
	}
}

func TestFetchUsesConditionalRequest(t *testing.T) {
	client := NewClient("test")
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("If-None-Match") != `"old"` || r.Header.Get("If-Modified-Since") == "" {
			t.Errorf("missing conditional headers: %#v", r.Header)
		}
		return response(http.StatusNotModified, nil, nil), nil
	})
	parsed, err := client.Fetch(context.Background(), domain.Feed{
		URL: "https://example.test/feed", ETag: `"old"`, LastModified: "Mon, 20 Jul 2026 10:00:00 GMT",
	})
	if err != nil || !parsed.NotModified {
		t.Fatalf("conditional fetch: %#v, %v", parsed, err)
	}
	if parsed.ETag != `"old"` || parsed.LastModified == "" {
		t.Fatalf("304 did not preserve validators: %#v", parsed)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func response(status int, body []byte, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(string(body))),
	}
}

func TestFetchRejectsUnsafeScheme(t *testing.T) {
	_, err := NewClient("test").Fetch(context.Background(), domain.Feed{URL: "file:///etc/passwd"})
	if err == nil {
		t.Fatal("expected unsafe scheme to fail")
	}
}

func TestFetchRejectsMalformedAndOversizedFeeds(t *testing.T) {
	for name, body := range map[string]string{"malformed": "<rss><broken>", "oversized": "123456"} {
		t.Run(name, func(t *testing.T) {
			client := NewClient("test")
			if name == "oversized" {
				client.maxBytes = 5
			}
			client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, []byte(body), nil), nil
			})
			if _, err := client.Fetch(context.Background(), domain.Feed{URL: "https://example.test/feed"}); err == nil {
				t.Fatal("expected fetch to fail")
			}
		})
	}
}
