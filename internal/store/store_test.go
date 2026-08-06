package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polera/rxs/internal/article"
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

func TestEnrichmentOverlayIsSearchableAndSurvivesRefreshState(t *testing.T) {
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
	parsed := domain.ParsedFeed{Title: "Example", Entries: []domain.Entry{{
		Identity: "guid:enriched", URL: "https://example.com/article", Title: "Overlay Article",
		HTML: "<p>Short feed summary.</p>", Text: "Short feed summary.",
		UpdatedAt: time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
	}}}
	if _, err := db.ApplyRefresh(ctx, source.ID, parsed); err != nil {
		t.Fatal(err)
	}
	candidates, err := db.EnrichmentCandidates(ctx, source.ID, 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates = %#v, err = %v", candidates, err)
	}
	entryID := candidates[0].ID
	if err := db.SetRead(ctx, entryID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.SetStarred(ctx, entryID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.SetReadingProgress(ctx, entryID, 0.63); err != nil {
		t.Fatal(err)
	}
	fullText := strings.Repeat("Detailed offline article text. ", 30) + "unique-enriched-phrase"
	content := article.Content{
		HTML: "<article><p>" + fullText + "</p></article>", Text: fullText,
		SourceURL: "https://example.com/article?canonical=1",
	}
	if err := db.SaveEnrichment(ctx, entryID, candidates[0].EnrichmentInputHash, content); err != nil {
		t.Fatal(err)
	}
	entries, err := db.Entries(ctx, domain.EntryFilter{Search: "unique-enriched-phrase"})
	if err != nil || len(entries) != 1 {
		t.Fatalf("search enriched entries = %#v, err = %v", entries, err)
	}
	if entries[0].ContentSource != domain.ContentSourceFullArticle || entries[0].Text != fullText ||
		!entries[0].Read || !entries[0].Starred || entries[0].ReadingProgress != 0.63 {
		t.Fatalf("enriched entry = %#v", entries[0])
	}

	parsed.Entries[0].Title = "Updated Overlay Article"
	if _, err := db.ApplyRefresh(ctx, source.ID, parsed); err != nil {
		t.Fatal(err)
	}
	entries, err = db.Entries(ctx, domain.EntryFilter{StarredOnly: true})
	if err != nil || len(entries) != 1 || entries[0].Text != fullText || entries[0].Title != "Updated Overlay Article" || !entries[0].Read {
		t.Fatalf("overlay after refresh = %#v, err = %v", entries, err)
	}
	candidates, err = db.EnrichmentCandidates(ctx, source.ID, 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("already attempted candidates = %#v, err = %v", candidates, err)
	}
}

func TestEnrichmentFailuresRetryOnlyAfterInputChanges(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, _ := db.AddFeed(ctx, "https://example.com/feed")
	parsed := domain.ParsedFeed{Entries: []domain.Entry{{
		Identity: "one", URL: "https://example.com/one", Title: "An Article",
		HTML: "<p>Short summary</p>", Text: "Short summary",
	}}}
	if _, err := db.ApplyRefresh(ctx, source.ID, parsed); err != nil {
		t.Fatal(err)
	}
	candidates, err := db.EnrichmentCandidates(ctx, source.ID, 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("initial candidates = %#v, err = %v", candidates, err)
	}
	if err := db.RecordEnrichmentError(ctx, candidates[0].ID, candidates[0].EnrichmentInputHash, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	candidates, err = db.EnrichmentCandidates(ctx, source.ID, 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("failed input repeated: %#v, err = %v", candidates, err)
	}
	entries, err := db.Entries(ctx, domain.EntryFilter{})
	if err != nil || entries[0].ContentSource != domain.ContentSourceFeed || entries[0].Text != "Short summary" {
		t.Fatalf("failed enrichment fallback = %#v, err = %v", entries, err)
	}

	parsed.Entries[0].URL = "https://example.com/changed"
	if _, err := db.ApplyRefresh(ctx, source.ID, parsed); err != nil {
		t.Fatal(err)
	}
	candidates, err = db.EnrichmentCandidates(ctx, source.ID, 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("changed input candidates = %#v, err = %v", candidates, err)
	}
}

func TestFailedReplacementKeepsLastSuccessfulOverlay(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, _ := db.AddFeed(ctx, "https://example.com/feed")
	parsed := domain.ParsedFeed{Entries: []domain.Entry{{
		Identity: "one", URL: "https://example.com/one", Title: "An Article",
		HTML: "<p>Short summary</p>", Text: "Short summary",
	}}}
	_, _ = db.ApplyRefresh(ctx, source.ID, parsed)
	candidates, _ := db.EnrichmentCandidates(ctx, source.ID, 10)
	fullText := strings.Repeat("Previously downloaded full text. ", 20)
	if err := db.SaveEnrichment(ctx, candidates[0].ID, candidates[0].EnrichmentInputHash,
		article.Content{HTML: "<p>" + fullText + "</p>", Text: fullText, SourceURL: parsed.Entries[0].URL}); err != nil {
		t.Fatal(err)
	}
	parsed.Entries[0].HTML = "<p>Changed short summary</p>"
	parsed.Entries[0].Text = "Changed short summary"
	_, _ = db.ApplyRefresh(ctx, source.ID, parsed)
	candidates, _ = db.EnrichmentCandidates(ctx, source.ID, 10)
	if len(candidates) != 1 {
		t.Fatalf("changed input candidates = %#v", candidates)
	}
	if err := db.RecordEnrichmentError(ctx, candidates[0].ID, candidates[0].EnrichmentInputHash, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	entries, err := db.Entries(ctx, domain.EntryFilter{})
	if err != nil || entries[0].ContentSource != domain.ContentSourceFullArticle || entries[0].Text != fullText {
		t.Fatalf("last successful overlay = %#v, err = %v", entries, err)
	}
	candidates, _ = db.EnrichmentCandidates(ctx, source.ID, 10)
	if len(candidates) != 0 {
		t.Fatalf("failed replacement repeated: %#v", candidates)
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
