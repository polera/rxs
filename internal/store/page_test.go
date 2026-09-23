package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/polera/rxs/internal/domain"
)

func TestEntriesPageAndHydration(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	feed, err := s.AddFeed(ctx, "https://example.test/feed")
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]domain.Entry, 137)
	for i := range entries {
		entries[i] = domain.Entry{
			Identity: fmt.Sprint(i), Title: fmt.Sprintf("Article %d", i),
			URL: fmt.Sprintf("https://example.test/%d", i), HTML: "<p>original</p>", Text: "original",
			// Shared dates exercise the ID tie break; zero dates exercise empty keys.
		}
		if i > 4 {
			entries[i].PublishedAt = time.Date(2026, 1, 1, 0, 0, i/4, 0, time.UTC)
		}
	}
	if _, err := s.ApplyRefresh(ctx, feed.ID, domain.ParsedFeed{Entries: entries}); err != nil {
		t.Fatal(err)
	}
	// Enriched text, rather than the original feed text, is searchable.
	if _, err := s.db.Exec(`INSERT INTO entry_content(entry_id, status, html, searchable_text, input_hash, attempted_at)
		VALUES (1, 'succeeded', '<p>expanded</p>', 'expanded', 'hash', '')`); err != nil {
		t.Fatal(err)
	}
	var seen []int64
	var pages [][]int64
	cursor := domain.EntryCursor{}
	for {
		page, err := s.EntriesPage(ctx, domain.EntryFilter{}, cursor, false, 13)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		var ids []int64
		for _, entry := range page {
			if !entry.Unloaded || entry.HTML != "" || entry.Text != "" {
				t.Fatalf("page included body: %#v", entry)
			}
			ids = append(ids, entry.ID)
			seen = append(seen, entry.ID)
		}
		pages = append(pages, ids)
		last := page[len(page)-1]
		cursor = domain.EntryCursor{Date: last.SortDate, ID: last.ID}
	}
	if len(seen) != len(entries) {
		t.Fatalf("seen %d of %d articles: %v", len(seen), len(entries), seen)
	}
	for i, id := range seen {
		if id != int64(len(entries)-i) {
			t.Fatalf("article %d has ID %d", i, id)
		}
	}
	// Walk backward from the start of every page, including empty-date ties.
	for i := 1; i < len(pages); i++ {
		first, err := s.Entry(ctx, pages[i][0])
		if err != nil {
			t.Fatal(err)
		}
		previous, err := s.EntriesPage(ctx, domain.EntryFilter{}, domain.EntryCursor{Date: first.SortDate, ID: first.ID}, true, 13)
		if err != nil {
			t.Fatal(err)
		}
		if len(previous) != len(pages[i-1]) {
			t.Fatalf("previous page %d: %d rows, want %d", i, len(previous), len(pages[i-1]))
		}
		for j := range previous {
			if previous[j].ID != pages[i-1][len(previous)-1-j] {
				t.Fatalf("previous page %d row %d: %d", i, j, previous[j].ID)
			}
		}
	}
	end, err := s.EntriesPage(ctx, domain.EntryFilter{}, domain.EntryCursor{}, true, 4)
	if err != nil || len(end) != 4 || end[0].ID != 1 {
		t.Fatalf("oldest page: %#v, %v", end, err)
	}
	full, err := s.Entry(ctx, 1)
	if err != nil || full.Unloaded || full.Text != "expanded" || full.HTML != "<p>expanded</p>" {
		t.Fatalf("hydrated entry: %#v, %v", full, err)
	}
	for _, tc := range []struct {
		query string
		want  int64
	}{
		{"expanded", 1},
		{"article 99", 100},
		{"orig", 137},
	} {
		page, err := s.EntriesPage(ctx, domain.EntryFilter{Search: tc.query}, domain.EntryCursor{}, false, 2)
		if err != nil || len(page) == 0 || page[0].ID != tc.want {
			t.Fatalf("search %q: %#v, %v", tc.query, page, err)
		}
	}
	if _, err := s.Entry(ctx, 10000); err == nil || !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("missing article: %v", err)
	}
}
