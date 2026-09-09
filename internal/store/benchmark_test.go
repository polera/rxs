package store

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/polera/rxs/internal/article"
	"github.com/polera/rxs/internal/domain"
)

func benchmarkStore(b *testing.B) (*Store, int64) {
	b.Helper()
	s, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	f, err := s.AddFeed(context.Background(), "https://example.com/feed")
	if err != nil {
		b.Fatal(err)
	}
	return s, f.ID
}

func BenchmarkRefresh(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		for _, initial := range []bool{true, false} {
			b.Run(fmt.Sprintf("%d/initial=%v", size, initial), func(b *testing.B) {
				s, feedID := benchmarkStore(b)
				ctx := context.Background()
				parsed := domain.ParsedFeed{Entries: make([]domain.Entry, size)}
				for i := range parsed.Entries {
					parsed.Entries[i] = domain.Entry{Identity: fmt.Sprint(i), URL: fmt.Sprintf("https://example.com/%d", i),
						Title: "An article", HTML: "<p>" + strings.Repeat("Summary text. ", 25) + "</p>", Text: strings.Repeat("Summary text. ", 25),
						UpdatedAt: time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)}
				}
				if !initial {
					if _, err := s.ApplyRefresh(ctx, feedID, parsed); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if initial {
						b.StopTimer()
						if _, err := s.db.Exec("DELETE FROM entries"); err != nil {
							b.Fatal(err)
						}
						b.StartTimer()
					}
					added, err := s.ApplyRefresh(ctx, feedID, parsed)
					if err != nil || (initial && added != size) || (!initial && added != 0) {
						b.Fatalf("added=%d err=%v", added, err)
					}
				}
			})
		}
	}
}

// Seed common schema columns so the same fixture runs before and after migration.
// Invalidate any derived metadata, then run the startup migration/backfill path.
func benchmarkHistory(b *testing.B, size int, mode string) (*Store, int64) {
	b.Helper()
	s, feedID := benchmarkStore(b)
	tx, err := s.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO entries(feed_id, identity, url, html, searchable_text, updated_at) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()
	attempt, err := tx.Prepare(`INSERT INTO entry_content(entry_id, status, input_hash, attempted_at) VALUES (?, 'failed', ?, '')`)
	if err != nil {
		b.Fatal(err)
	}
	defer attempt.Close()
	text := strings.Repeat("Summary text. ", 25)
	if mode == "none" {
		text = strings.Repeat("Full article text. ", 120)
	}
	html := "<p>" + text + "</p><!--" + strings.Repeat("x", 2048) + "-->"
	for i := 0; i < size; i++ {
		updated := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)
		result, err := stmt.Exec(feedID, fmt.Sprint(i), "https://example.com/article", html, text, formatTime(updated))
		if err != nil {
			b.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			b.Fatal(err)
		}
		if mode == "attempted" || (mode == "older-pending" && i >= 10) {
			if _, err := attempt.Exec(id, article.InputHash("https://example.com/article", html, updated)); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	var metadataTable bool
	if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='store_metadata')").Scan(&metadataTable); err != nil {
		b.Fatal(err)
	}
	if metadataTable {
		if _, err := s.db.Exec("UPDATE store_metadata SET value=0 WHERE key='enrichment_policy'"); err != nil {
			b.Fatal(err)
		}
	}
	// migrate is idempotent and also refreshes stale derived metadata, if supported.
	if err := s.migrate(context.Background()); err != nil {
		b.Fatal(err)
	}
	return s, feedID
}

func BenchmarkEnrichmentHistory(b *testing.B) {
	for _, size := range []int{1000, 10000, 100000} {
		for _, mode := range []string{"none", "attempted", "older-pending"} {
			b.Run(fmt.Sprintf("%d/%s", size, mode), func(b *testing.B) {
				s, feedID := benchmarkHistory(b, size, mode)
				want := 0
				if mode == "older-pending" {
					want = 10
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					got, err := s.EnrichmentCandidates(context.Background(), feedID, 10)
					if err != nil || len(got) != want {
						b.Fatalf("candidates=%d want=%d err=%v", len(got), want, err)
					}
				}
			})
		}
	}
}

func BenchmarkEntriesHistory(b *testing.B) {
	for _, size := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			s, feedID := benchmarkHistory(b, size, "none")
			for _, filter := range []domain.EntryFilter{{Limit: 1000}, {FeedID: feedID, Limit: 1000}} {
				b.Run(fmt.Sprintf("feed=%d", filter.FeedID), func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						entries, err := s.Entries(context.Background(), filter)
						if err != nil || len(entries) != 1000 {
							b.Fatalf("entries=%d err=%v", len(entries), err)
						}
					}
				})
			}
		})
	}
}

func BenchmarkSetReadDuringEnrichment(b *testing.B) {
	for _, size := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			s, feedID := benchmarkHistory(b, size, "attempted")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				for ctx.Err() == nil {
					if _, err := s.EnrichmentCandidates(ctx, feedID, 10); err != nil && ctx.Err() == nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			// Start timing only once selection owns the sole connection.
			for s.db.Stats().InUse == 0 {
				select {
				case err := <-done:
					b.Fatalf("selection stopped before contention: %v", err)
				default:
				}
				runtime.Gosched()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.SetRead(context.Background(), 1, i%2 == 0); err != nil {
					b.Error(err)
					break
				}
			}
			b.StopTimer()
			cancel()
			if err := <-done; err != nil {
				b.Fatal(err)
			}
		})
	}
}

func BenchmarkIndexFootprint(b *testing.B) {
	for _, mode := range []string{"none", "attempted"} {
		b.Run(mode, func(b *testing.B) { benchmarkIndexFootprint(b, mode) })
	}
}

func benchmarkIndexFootprint(b *testing.B, mode string) {
	s, _ := benchmarkHistory(b, 10000, mode)
	var pages, pageSize, free int64
	for query, dest := range map[string]*int64{"PRAGMA page_count": &pages, "PRAGMA page_size": &pageSize, "PRAGMA freelist_count": &free} {
		if err := s.db.QueryRow(query).Scan(dest); err != nil {
			b.Fatal(err)
		}
	}
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='entries' AND sql IS NOT NULL`)
	if err != nil {
		b.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			b.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Close(); err != nil {
		b.Fatal(err)
	}
	// Time the footprint probe itself, not fixture creation or backfill. Sizes
	// are allocated index pages, measured by transactional DROP then rollback.
	b.ReportAllocs()
	b.ResetTimer()
	b.ReportMetric(float64((pages-free)*pageSize), "database-bytes")
	for i := 0; i < b.N; i++ {
		for _, name := range names {
			tx, err := s.db.Begin()
			if err != nil {
				b.Fatal(err)
			}
			if _, err := tx.Exec(`DROP INDEX "` + name + `"`); err != nil {
				b.Fatal(err)
			}
			var after int64
			if err := tx.QueryRow("PRAGMA freelist_count").Scan(&after); err != nil {
				b.Fatal(err)
			}
			if err := tx.Rollback(); err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64((after-free)*pageSize), name+"-bytes")
		}
	}
}

func BenchmarkRefreshIndexWrites(b *testing.B) {
	for _, indexes := range []string{"current", "with-legacy", "without-global-date", "without-candidate"} {
		b.Run(indexes, func(b *testing.B) {
			s, feedID := benchmarkStore(b)
			var ddl string
			switch indexes {
			case "with-legacy":
				ddl = "CREATE INDEX entries_feed_date ON entries(feed_id, published_at DESC, id DESC)"
			case "without-global-date":
				ddl = "DROP INDEX entries_effective_date"
			case "without-candidate":
				ddl = "DROP INDEX entries_enrichment_pending"
			}
			if ddl != "" {
				if _, err := s.db.Exec(ddl); err != nil {
					b.Fatal(err)
				}
			}
			parsed := domain.ParsedFeed{Entries: make([]domain.Entry, 1000)}
			for i := range parsed.Entries {
				parsed.Entries[i] = domain.Entry{Identity: fmt.Sprint(i), URL: "https://example.com/article", HTML: strings.Repeat("summary ", 40), Text: "summary",
					UpdatedAt: time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)}
			}
			if _, err := s.ApplyRefresh(context.Background(), feedID, parsed); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.ApplyRefresh(context.Background(), feedID, parsed); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
