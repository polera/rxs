package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/polera/rxs/internal/domain"
)

func TestRefreshIsIdempotentAndPreservesState(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "rxs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := db.AddFeed(ctx, "https://example.com/feed")
	if err != nil {
		t.Fatal(err)
	}
	parsed := domain.ParsedFeed{Title: "Example", ETag: `"one"`, Entries: []domain.Entry{{
		Identity: "guid:1", URL: "https://example.com/1", Title: "Original",
		PublishedAt: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), Text: "searchable body",
	}}}
	added, err := db.ApplyRefresh(ctx, source.ID, parsed)
	if err != nil || added != 1 {
		t.Fatalf("first refresh: added=%d err=%v", added, err)
	}
	entries, err := db.Entries(ctx, domain.EntryFilter{Search: "body"})
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries: len=%d err=%v", len(entries), err)
	}
	if err := db.SetRead(ctx, entries[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.SetStarred(ctx, entries[0].ID, true); err != nil {
		t.Fatal(err)
	}
	parsed.Entries[0].Title = "Updated"
	added, err = db.ApplyRefresh(ctx, source.ID, parsed)
	if err != nil || added != 0 {
		t.Fatalf("second refresh: added=%d err=%v", added, err)
	}
	entries, err = db.Entries(ctx, domain.EntryFilter{StarredOnly: true})
	if err != nil || len(entries) != 1 || !entries[0].Read || entries[0].Title != "Updated" {
		t.Fatalf("state after upsert: %#v, err=%v", entries, err)
	}
	feeds, err := db.Feeds(ctx)
	if err != nil || len(feeds) != 1 || feeds[0].UnreadCount != 0 {
		t.Fatalf("feed count: %#v, err=%v", feeds, err)
	}
}

func TestDeleteFeedCascades(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, _ := db.AddFeed(ctx, "https://example.com/feed")
	_, err = db.ApplyRefresh(ctx, source.ID, domain.ParsedFeed{Entries: []domain.Entry{{Identity: "one"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteFeed(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := db.Entries(ctx, domain.EntryFilter{})
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries after delete: %v, %v", entries, err)
	}
}

func TestReadingProgressPersistsAndIsClamped(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "rxs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := db.AddFeed(ctx, "https://example.com/feed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyRefresh(ctx, source.ID, domain.ParsedFeed{
		Entries: []domain.Entry{{Identity: "one", Title: "Article"}},
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := db.Entries(ctx, domain.EntryFilter{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries: %#v, err=%v", entries, err)
	}

	if err := db.SetReadingProgress(ctx, entries[0].ID, 0.42); err != nil {
		t.Fatal(err)
	}
	entries, err = db.Entries(ctx, domain.EntryFilter{})
	if err != nil || entries[0].ReadingProgress != 0.42 {
		t.Fatalf("saved progress = %v, err=%v", entries[0].ReadingProgress, err)
	}

	if err := db.SetReadingProgress(ctx, entries[0].ID, 2); err != nil {
		t.Fatal(err)
	}
	entries, err = db.Entries(ctx, domain.EntryFilter{})
	if err != nil || entries[0].ReadingProgress != 1 {
		t.Fatalf("clamped progress = %v, err=%v", entries[0].ReadingProgress, err)
	}
}

func TestAddExistingFeedReturnsTheExistingFeed(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, err := db.AddFeed(ctx, "https://example.com/first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddFeed(ctx, "https://example.com/second"); err != nil {
		t.Fatal(err)
	}
	again, err := db.AddFeed(ctx, first.URL)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Fatalf("duplicate feed ID = %d, want %d", again.ID, first.ID)
	}
}
