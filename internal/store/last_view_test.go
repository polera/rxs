package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/polera/rxs/internal/domain"
)

func TestLastViewReopensAndSeeksPastFirstPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	feed, err := s.AddFeed(ctx, "https://example.test/feed")
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]domain.Entry, 140)
	for i := range entries {
		entries[i] = domain.Entry{Identity: fmt.Sprint(i), HTML: "<p>body</p>",
			PublishedAt: time.Date(2026, 1, 1, 0, 0, i/3, 0, time.UTC)}
	}
	if _, err := s.ApplyRefresh(ctx, feed.ID, domain.ParsedFeed{Entries: entries}); err != nil {
		t.Fatal(err)
	}
	view := domain.LastView{Pane: "reader", Scope: "feed", FeedID: feed.ID, FeedURL: feed.URL,
		EntryID: 12, EntryFeedURL: feed.URL, EntryIdentity: "11"}
	if err := s.SaveLastView(ctx, view); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRead(ctx, 12, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, found, err := s.LoadLastView(ctx)
	if err != nil || !found || got != view {
		t.Fatalf("saved view: %#v, found=%t, err=%v", got, found, err)
	}
	filter := domain.EntryFilter{FeedID: feed.ID, UnreadOnly: true}
	page, previous, next, found, err := s.EntryPageAt(ctx, filter, 12, 64)
	if err != nil || found || len(page) != 0 || previous || next {
		t.Fatalf("hidden read article was visible: %v, %t %t %t, %v", page, previous, next, found, err)
	}
	filter.UnreadOnly = false
	page, previous, next, found, err = s.EntryPageAt(ctx, filter, 12, 64)
	if err != nil || !found || len(page) != 12 || page[0].ID != 12 || !previous || next || !page[0].Unloaded {
		t.Fatalf("seek page: %#v, prev=%t next=%t found=%t err=%v", page, previous, next, found, err)
	}
	if _, _, _, found, err := s.EntryPageAt(ctx, domain.EntryFilter{FeedID: feed.ID + 1}, 12, 64); err != nil || found {
		t.Fatalf("wrong feed matched: found=%t err=%v", found, err)
	}
	page, previous, next, found, err = s.EntryPageAt(ctx, filter, 100, 64)
	if err != nil || !found || !previous || !next || len(page) != 64 || page[0].ID != 100 || page[63].ID != 37 {
		t.Fatalf("middle page: first=%#v count=%d prev=%t next=%t found=%t err=%v", page, len(page), previous, next, found, err)
	}
}

func TestLastViewRejectsReusedFeedAndArticleIDs(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	feed, err := s.AddFeed(ctx, "https://example.test/old")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyRefresh(ctx, feed.ID, domain.ParsedFeed{Entries: []domain.Entry{{Identity: "old"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveLastView(ctx, domain.LastView{Pane: "reader", Scope: "feed", FeedID: feed.ID, EntryID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFeed(ctx, feed.ID); err != nil {
		t.Fatal(err)
	}
	newFeed, err := s.AddFeed(ctx, "https://example.test/new")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyRefresh(ctx, newFeed.ID, domain.ParsedFeed{Entries: []domain.Entry{{Identity: "new"}}}); err != nil {
		t.Fatal(err)
	}
	view, found, err := s.LoadLastView(ctx)
	if err != nil || !found || view.Scope != "all" || view.EntryID != 0 {
		t.Fatalf("stale IDs restored: %#v, found=%t err=%v", view, found, err)
	}
}
