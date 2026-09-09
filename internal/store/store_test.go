package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/polera/rxs/internal/article"
	"github.com/polera/rxs/internal/domain"
)

func TestOpenLiteralFilenames(t *testing.T) {
	for _, name := range []string{"with spaces.db", "with#fragment.db", "literal%2F.db", "query?mode=memory&cache=shared.db"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.Contains(name, "?") {
				t.Skip("Windows filenames cannot contain question marks")
			}
			dir := t.TempDir()
			t.Chdir(dir)
			path := filepath.Join(dir, name)
			db, err := Open(name)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			checkPragmas(t, db, "wal")
			if _, err := db.AddFeed(context.Background(), "https://example.com/feed"); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 1 || files[0].Name() != name {
				t.Fatalf("literal filename: files=%v err=%v, want only %q", files, err, name)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			feeds, err := reopened.Feeds(context.Background())
			if err != nil || len(feeds) != 1 || feeds[0].URL != "https://example.com/feed" {
				t.Fatalf("reopened feeds=%v err=%v", feeds, err)
			}
		})
	}
}

func TestMemoryStoresAreIsolated(t *testing.T) {
	ctx := context.Background()
	first, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	checkPragmas(t, first, "memory")
	checkPragmas(t, second, "memory")
	for i, db := range []*Store{first, second} {
		feeds, err := db.Feeds(ctx)
		if err != nil || len(feeds) != 0 {
			t.Fatalf("store %d initial feeds=%v err=%v", i, feeds, err)
		}
		if _, err := db.AddFeed(ctx, "https://example.com/feed"); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.DeleteFeed(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	feeds, err := second.Feeds(ctx)
	if err != nil || len(feeds) != 1 {
		t.Fatalf("second store after first closes: feeds=%v err=%v", feeds, err)
	}
}

func checkPragmas(t *testing.T, db *Store, journalMode string) {
	t.Helper()
	for pragma, want := range map[string]string{"foreign_keys": "1", "journal_mode": journalMode, "busy_timeout": "5000"} {
		var got string
		if err := db.db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
			t.Fatalf("PRAGMA %s = %q, err=%v, want %q", pragma, got, err, want)
		}
	}
	if got := db.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections=%d, want 1", got)
	}
}

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
	if applied, err := db.SaveEnrichment(ctx, candidates[0], content); err != nil || !applied {
		t.Fatalf("save enrichment: applied=%v err=%v", applied, err)
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
	if applied, err := db.RecordEnrichmentError(ctx, candidates[0], context.DeadlineExceeded); err != nil || !applied {
		t.Fatalf("record enrichment error: applied=%v err=%v", applied, err)
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
	if applied, err := db.SaveEnrichment(ctx, candidates[0],
		article.Content{HTML: "<p>" + fullText + "</p>", Text: fullText, SourceURL: parsed.Entries[0].URL}); err != nil || !applied {
		t.Fatalf("save enrichment: applied=%v err=%v", applied, err)
	}
	parsed.Entries[0].HTML = "<p>Changed short summary</p>"
	parsed.Entries[0].Text = "Changed short summary"
	_, _ = db.ApplyRefresh(ctx, source.ID, parsed)
	candidates, _ = db.EnrichmentCandidates(ctx, source.ID, 10)
	if len(candidates) != 1 {
		t.Fatalf("changed input candidates = %#v", candidates)
	}
	if applied, err := db.RecordEnrichmentError(ctx, candidates[0], context.DeadlineExceeded); err != nil || !applied {
		t.Fatalf("record enrichment error: applied=%v err=%v", applied, err)
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

func TestEnrichmentRejectsChangedInputAndIdentity(t *testing.T) {
	for _, change := range []string{"url", "html", "updated_at", "entry identity", "feed url", "feed id", "deleted", "readded"} {
		for _, failure := range []bool{false, true} {
			name := change + "/success"
			if failure {
				name = change + "/failure"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				db, err := Open(":memory:")
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				source, err := db.AddFeed(ctx, "https://example.com/feed")
				if err != nil {
					t.Fatal(err)
				}
				parsed := domain.ParsedFeed{Entries: []domain.Entry{{
					Identity: "one", URL: "https://example.com/one", Title: "Article",
					HTML: "<p>Summary</p>", Text: "Summary",
				}}}
				if _, err := db.ApplyRefresh(ctx, source.ID, parsed); err != nil {
					t.Fatal(err)
				}
				candidates, err := db.EnrichmentCandidates(ctx, source.ID, 1)
				if err != nil || len(candidates) != 1 {
					t.Fatalf("candidates=%v err=%v", candidates, err)
				}
				candidate := candidates[0]
				switch change {
				case "url", "html", "updated_at":
					_, err = db.db.ExecContext(ctx, "UPDATE entries SET "+change+"=? WHERE id=?", "2026-09-06T12:00:00Z", candidate.ID)
				case "entry identity":
					// Reuse the entry rowid with identical extraction input, but a new GUID.
					if _, err := db.db.ExecContext(ctx, "DELETE FROM entries WHERE id=?", candidate.ID); err != nil {
						t.Fatal(err)
					}
					parsed.Entries[0].Identity = "two"
					_, err = db.ApplyRefresh(ctx, source.ID, parsed)
				case "feed url":
					// Isolate the natural feed identity check from its creation timestamp.
					_, err = db.db.ExecContext(ctx, "UPDATE feeds SET url=? WHERE id=?", "https://other.example/feed", source.ID)
				case "feed id":
					var other domain.Feed
					other, err = db.AddFeed(ctx, "https://other.example/feed")
					if err == nil {
						_, err = db.db.ExecContext(ctx, "UPDATE entries SET feed_id=? WHERE id=?", other.ID, candidate.ID)
					}
				case "deleted", "readded":
					if err := db.DeleteFeed(ctx, source.ID); err != nil {
						t.Fatal(err)
					}
					if change == "readded" {
						var replacement domain.Feed
						replacement, err = db.AddFeed(ctx, source.URL)
						if err != nil || replacement.ID != source.ID {
							t.Fatalf("feed rowid not reused: replacement=%v err=%v", replacement, err)
						}
						_, err = db.ApplyRefresh(ctx, replacement.ID, parsed)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if change == "entry identity" || change == "readded" {
					current, err := db.EnrichmentCandidates(ctx, source.ID, 1)
					if err != nil || len(current) != 1 || current[0].ID != candidate.ID || current[0].EnrichmentInputHash != candidate.EnrichmentInputHash {
						t.Fatalf("expected reused entry rowid and identical input: candidates=%v err=%v", current, err)
					}
				}
				var applied bool
				if failure {
					applied, err = db.RecordEnrichmentError(ctx, candidate, context.DeadlineExceeded)
				} else {
					applied, err = db.SaveEnrichment(ctx, candidate, article.Content{HTML: "old overlay", Text: "old overlay"})
				}
				if err != nil || applied {
					t.Fatalf("stale completion: applied=%v err=%v", applied, err)
				}
				var count int
				if err := db.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM entry_content").Scan(&count); err != nil || count != 0 {
					t.Fatalf("stale completion wrote content: count=%d err=%v", count, err)
				}
			})
		}
	}
}

func TestEnrichmentCompletionCannotOverwriteAcceptedInput(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source, err := db.AddFeed(ctx, "https://example.com/feed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyRefresh(ctx, source.ID, domain.ParsedFeed{Entries: []domain.Entry{{
		Identity: "one", URL: "https://example.com/one", HTML: "<p>Summary</p>",
	}}}); err != nil {
		t.Fatal(err)
	}
	candidates, err := db.EnrichmentCandidates(ctx, source.ID, 1)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates=%v err=%v", candidates, err)
	}
	candidate := candidates[0]
	if applied, err := db.SaveEnrichment(ctx, candidate, article.Content{HTML: "new overlay", Text: "new overlay"}); err != nil || !applied {
		t.Fatalf("new completion: applied=%v err=%v", applied, err)
	}
	var before, after string
	const snapshot = `SELECT json_array(status, html, searchable_text, source_url, input_hash, attempted_at, fetched_at, last_error) FROM entry_content`
	if err := db.db.QueryRowContext(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if applied, err := db.SaveEnrichment(ctx, candidate, article.Content{HTML: "old overlay", Text: "old overlay"}); err != nil || applied {
		t.Fatalf("old success: applied=%v err=%v", applied, err)
	}
	if applied, err := db.RecordEnrichmentError(ctx, candidate, context.DeadlineExceeded); err != nil || applied {
		t.Fatalf("old failure: applied=%v err=%v", applied, err)
	}
	if err := db.db.QueryRowContext(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatalf("accepted result changed: before=%s after=%s err=%v", before, after, err)
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
