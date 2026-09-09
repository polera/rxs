package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/polera/rxs/internal/article"
	"github.com/polera/rxs/internal/domain"
)

func legacyStore(t *testing.T, path string) *Store {
	t.Helper()
	dsn, err := databaseDSN(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for i, name := range []string{"001_initial.sql", "002_reading_progress.sql", "003_entry_content.sql"} {
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO schema_migrations(version) VALUES (?)", i+1); err != nil {
			t.Fatal(err)
		}
	}
	return &Store{db: db}
}

func TestDateMigrationOrderingAndHashes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	s := legacyStore(t, path)
	if _, err := s.db.Exec(`INSERT INTO feeds(url, title) VALUES ('https://example.com/feed', 'Legacy')`); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct{ published, updated string }{
		{"", ""},
		{"2026-01-01T00:00:00Z", "2026-02-01T00:00:00Z"}, // Publication wins over update.
		{"2026-01-01T00:00:00.1Z", ""},
		{"", "2026-01-01T01:00:00.100000001+01:00"},
		{"2025-12-31T19:00:00.100000001-05:00", ""}, // Tie: higher ID first.
	}
	var pendingHash string
	for i, fixture := range fixtures {
		id := i + 1
		hash := article.InputHash("https://example.com/article", "<p>Summary</p>", parseTime(fixture.updated))
		if _, err := s.db.Exec(`INSERT INTO entries(id, feed_id, identity, url, html, searchable_text, published_at, updated_at)
 VALUES (?, 1, ?, 'https://example.com/article', '<p>Summary</p>', 'Summary', ?, ?)`, id, fmt.Sprint(id), fixture.published, fixture.updated); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO entry_state(entry_id, is_read, is_starred, reading_progress) VALUES (?, 1, 1, 0.7)`, id); err != nil {
			t.Fatal(err)
		}
		if id != 4 {
			if _, err := s.db.Exec(`INSERT INTO entry_content(entry_id, status, html, searchable_text, input_hash, attempted_at, fetched_at)
 VALUES (?, 'succeeded', 'overlay', 'overlay', ?, '2026-01-01T00:00:00.123456789Z', '2026-01-01T00:00:00Z')`, id, hash); err != nil {
				t.Fatal(err)
			}
		} else {
			pendingHash = hash
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, feedID := range []int64{0, 1} {
		entries, err := s.Entries(ctx, domain.EntryFilter{FeedID: feedID})
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, entry := range entries {
			ids = append(ids, entry.ID)
			if !entry.Read || !entry.Starred || entry.ReadingProgress != 0.7 || (entry.ID != 4 && entry.HTML != "overlay") {
				t.Fatalf("state or overlay changed: %+v", entry)
			}
		}
		if !reflect.DeepEqual(ids, []int64{5, 4, 3, 2, 1}) {
			t.Fatalf("feed %d ordering=%v", feedID, ids)
		}
	}
	for i, fixture := range fixtures {
		var published, updated, hash string
		if err := s.db.QueryRow(`SELECT published_at, updated_at, enrichment_input_hash FROM entries WHERE id=?`, i+1).Scan(&published, &updated, &hash); err != nil {
			t.Fatal(err)
		}
		if published != formatTime(parseTime(fixture.published)) || updated != formatTime(parseTime(fixture.updated)) ||
			hash != article.InputHash("https://example.com/article", "<p>Summary</p>", parseTime(fixture.updated)) {
			t.Fatalf("normalization changed instant/hash: %q %q %q", published, updated, hash)
		}
	}
	var created, fetched, attempted string
	if err := s.db.QueryRow(`SELECT f.created_at, ec.fetched_at, ec.attempted_at FROM feeds f JOIN entry_content ec ON ec.entry_id=1`).Scan(&created, &fetched, &attempted); err != nil {
		t.Fatal(err)
	}
	if len(created) != 30 || fetched != "2026-01-01T00:00:00.000000000Z" || attempted != "2026-01-01T00:00:00.123456789Z" {
		t.Fatalf("other dates: created=%q fetched=%q attempted=%q", created, fetched, attempted)
	}
	if _, err := s.ApplyRefresh(ctx, 1, domain.ParsedFeed{NotModified: true}); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.EnrichmentCandidates(ctx, 1, 10)
	if err != nil || len(candidates) != 1 || candidates[0].ID != 4 || candidates[0].EnrichmentInputHash != pendingHash {
		t.Fatalf("304 backfill: candidates=%+v err=%v", candidates, err)
	}
	if applied, err := s.SaveEnrichment(ctx, candidates[0], article.Content{HTML: "full", Text: "full"}); err != nil || !applied {
		t.Fatalf("normalized candidate rejected: applied=%v err=%v", applied, err)
	}
	// An unchanged refresh with the original offset must not schedule another attempt.
	if _, err := s.ApplyRefresh(ctx, 1, domain.ParsedFeed{Entries: []domain.Entry{{Identity: "4", URL: "https://example.com/article",
		HTML: "<p>Summary</p>", Text: "Summary", UpdatedAt: parseTime(fixtures[3].updated)}}}); err != nil {
		t.Fatal(err)
	}
	if candidates, err := s.EnrichmentCandidates(ctx, 1, 10); err != nil || len(candidates) != 0 {
		t.Fatalf("hash changed after normalized refresh: candidates=%+v err=%v", candidates, err)
	}
}

func TestDateMigrationRollsBackInvalidLegacyDate(t *testing.T) {
	s := legacyStore(t, ":memory:")
	if _, err := s.db.Exec(`INSERT INTO feeds(url) VALUES ('https://example.com/feed');
 WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<300)
 INSERT INTO entries(feed_id, identity, published_at)
 SELECT 1, CAST(i AS TEXT), CASE WHEN i=300 THEN 'invalid' ELSE '2026-01-01T00:00:00Z' END FROM n`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(context.Background()); err == nil || !strings.Contains(err.Error(), "normalize entries.published_at") {
		t.Fatalf("migration error=%v", err)
	}
	var date string
	var version int
	if err := s.db.QueryRow("SELECT published_at FROM entries WHERE id=1").Scan(&date); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if date != "2026-01-01T00:00:00Z" || version != 3 {
		t.Fatalf("partial migration: date=%q version=%d", date, version)
	}
}

func TestFixedUTCDateFormat(t *testing.T) {
	if got := formatTime(time.Time{}); got != "" {
		t.Fatalf("zero date=%q", got)
	}
	previous := ""
	for _, nanos := range []int{0, 1, 100000000, 999999999} {
		instant := time.Date(2026, 1, 1, 1, 0, 0, nanos, time.FixedZone("offset", 3600))
		got := formatTime(instant)
		if len(got) != 30 || !strings.HasSuffix(got, "Z") || !parseTime(got).Equal(instant) || got <= previous {
			t.Fatalf("date %v formatted=%q previous=%q", instant, got, previous)
		}
		previous = got
	}
}

func TestEnrichmentPolicyBackfillIsAtomicAndPreservesAttempts(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f, _ := s.AddFeed(ctx, "https://example.com/feed")
	parsed := domain.ParsedFeed{Entries: make([]domain.Entry, 600)} // Cross batch boundaries.
	for i := range parsed.Entries {
		parsed.Entries[i] = domain.Entry{Identity: fmt.Sprint(i), URL: "https://example.com/article", HTML: "summary", Text: "summary"}
	}
	if _, err := s.ApplyRefresh(ctx, f.ID, parsed); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.EnrichmentCandidates(ctx, f.ID, 1)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	if applied, err := s.RecordEnrichmentError(ctx, candidates[0], context.DeadlineExceeded); err != nil || !applied {
		t.Fatalf("record attempt: applied=%v err=%v", applied, err)
	}
	if _, err := s.db.Exec(`UPDATE entries SET enrichment_eligible=0, enrichment_input_hash='obsolete';
 UPDATE store_metadata SET value=0 WHERE key='enrichment_policy';
 CREATE TRIGGER fail_backfill BEFORE UPDATE ON entries WHEN OLD.id=300 BEGIN SELECT RAISE(ABORT, 'backfill failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err == nil {
		t.Fatal("backfill should fail")
	}
	var count, version int
	if err := s.db.QueryRow("SELECT count(*) FROM entries WHERE enrichment_input_hash='obsolete'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT value FROM store_metadata WHERE key='enrichment_policy'").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if count != 600 || version != 0 {
		t.Fatalf("partial backfill count=%d version=%d", count, version)
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_backfill"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	candidates, err = s.EnrichmentCandidates(ctx, f.ID, 1000)
	if err != nil || len(candidates) != 599 {
		t.Fatalf("backfill candidates=%d err=%v", len(candidates), err)
	}
	if err := s.db.QueryRow("SELECT value FROM store_metadata WHERE key='enrichment_policy'").Scan(&version); err != nil || version != enrichmentPolicyVersion {
		t.Fatalf("policy version=%d err=%v", version, err)
	}
	// Current-policy opens must not read or rewrite bodies again.
	if _, err := s.db.Exec(`CREATE TRIGGER no_backfill BEFORE UPDATE ON entries BEGIN SELECT RAISE(ABORT, 'unnecessary backfill'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestEnrichmentSQLLimitDoesNotStarveOlderEntries(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f, _ := s.AddFeed(ctx, "https://example.com/feed")
	parsed := domain.ParsedFeed{Entries: make([]domain.Entry, 40)}
	for i := range parsed.Entries {
		parsed.Entries[i] = domain.Entry{Identity: fmt.Sprint(i), URL: "https://example.com/article", HTML: "summary", Text: "summary",
			UpdatedAt: time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)}
		if i >= 30 {
			parsed.Entries[i].Text = strings.Repeat("Full article text. ", 200)
		}
	}
	if _, err := s.ApplyRefresh(ctx, f.ID, parsed); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, -1} {
		if got, err := s.EnrichmentCandidates(ctx, f.ID, limit); err != nil || len(got) != 0 {
			t.Fatalf("limit %d: count=%d err=%v", limit, len(got), err)
		}
	}
	for round := 0; round < 3; round++ {
		got, err := s.EnrichmentCandidates(ctx, f.ID, 10)
		if err != nil || len(got) != 10 || got[0].Identity != fmt.Sprint(29-round*10) {
			t.Fatalf("round %d: candidates=%+v err=%v", round, got, err)
		}
		for _, candidate := range got {
			if applied, err := s.RecordEnrichmentError(ctx, candidate, context.DeadlineExceeded); err != nil || !applied {
				t.Fatalf("applied=%v err=%v", applied, err)
			}
		}
	}
	if got, err := s.EnrichmentCandidates(ctx, f.ID, 10); err != nil || len(got) != 0 {
		t.Fatalf("all attempted: count=%d err=%v", len(got), err)
	}
	// Eligibility must update even when only searchable text (not the input hash) changes.
	parsed.Entries[39].Text = "new summary"
	if _, err := s.ApplyRefresh(ctx, f.ID, parsed); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EnrichmentCandidates(ctx, f.ID, 10); err != nil || len(got) != 1 || got[0].Identity != "39" {
		t.Fatalf("changed eligibility: candidates=%+v err=%v", got, err)
	}
}

func TestRefreshDuplicateIdentityAndRollback(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f, _ := s.AddFeed(ctx, "https://example.com/feed")
	entry := domain.Entry{Identity: "one", URL: "https://example.com/article", Title: "first", HTML: "summary", Text: "summary"}
	updated := entry
	updated.Title, updated.Author = "last", "author"
	if added, err := s.ApplyRefresh(ctx, f.ID, domain.ParsedFeed{Title: "saved", Entries: []domain.Entry{entry, updated}}); err != nil || added != 1 {
		t.Fatalf("duplicate refresh: added=%d err=%v", added, err)
	}
	entries, err := s.Entries(ctx, domain.EntryFilter{})
	if err != nil || len(entries) != 1 || entries[0].Title != "last" || entries[0].Author != "author" {
		t.Fatalf("last duplicate wins: entries=%+v err=%v", entries, err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_state BEFORE INSERT ON entry_state WHEN NEW.entry_id<>1 BEGIN SELECT RAISE(ABORT, 'state failure'); END`); err != nil {
		t.Fatal(err)
	}
	second := entry
	second.Identity = "two"
	if added, err := s.ApplyRefresh(ctx, f.ID, domain.ParsedFeed{Title: "rollback", Entries: []domain.Entry{entry, second}}); err == nil || added != 0 {
		t.Fatalf("failed refresh: added=%d err=%v", added, err)
	}
	entries, err = s.Entries(ctx, domain.EntryFilter{})
	if err != nil || len(entries) != 1 || entries[0].Title != "last" {
		t.Fatalf("rollback entries=%+v err=%v", entries, err)
	}
	feed, err := s.Feed(ctx, f.ID)
	if err != nil || feed.Title != "saved" {
		t.Fatalf("rollback feed=%+v err=%v", feed, err)
	}
}

func TestOrderingQueryPlans(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`INSERT INTO feeds(url) VALUES ('https://example.com/one'), ('https://example.com/two');
 WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<512)
 INSERT INTO entries(feed_id, identity, updated_at, html, enrichment_input_hash, enrichment_eligible)
 SELECT 1+i%2, CAST(i AS TEXT), '2026-01-01T00:00:00.000000000Z', 'summary', 'hash', i%3=0 FROM n`); err != nil {
		t.Fatal(err)
	}
	for _, analyzed := range []bool{false, true} {
		if analyzed {
			if _, err := s.db.Exec("ANALYZE"); err != nil {
				t.Fatal(err)
			}
		}
		for _, test := range []struct {
			name, query, index string
			args               []any
		}{
			{"all", entrySelect + " ORDER BY " + effectiveDateSQL + " DESC, e.id DESC LIMIT ?", "entries_effective_date", []any{1000}},
			{"feed", entrySelect + " AND e.feed_id=? ORDER BY " + effectiveDateSQL + " DESC, e.id DESC LIMIT ?", "entries_feed_effective_date", []any{1, 1000}},
			{"unread", entrySelect + " AND e.feed_id=? AND COALESCE(es.is_read,0)=0 ORDER BY " + effectiveDateSQL + " DESC, e.id DESC LIMIT ?", "entries_feed_effective_date", []any{1, 1000}},
			{"enrichment", enrichmentCandidatesSQL, "entries_enrichment_pending", []any{1, 10}},
		} {
			t.Run(fmt.Sprintf("%s/analyzed=%v", test.name, analyzed), func(t *testing.T) {
				rows, err := s.db.Query("EXPLAIN QUERY PLAN "+test.query, test.args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var plan []string
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
						t.Fatal(err)
					}
					plan = append(plan, detail)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				text := strings.Join(plan, "\n")
				t.Log(text)
				if !strings.Contains(text, "USING INDEX "+test.index) || strings.Contains(text, "TEMP B-TREE") {
					t.Fatalf("wrong index or temporary sort:\n%s", text)
				}
			})
		}
	}
}
