package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/polera/rxs/internal/domain"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is empty")
	}
	dsn := "file:" + filepath.ToSlash(path)
	if path == ":memory:" {
		dsn = "file:rxs-memory?mode=memory&cache=shared"
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	dsn += separator + url.Values{
		"_pragma": []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)"},
	}.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
    )`); err != nil {
		return fmt.Errorf("prepare migrations: %w", err)
	}
	files, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	for _, file := range files {
		versionText, _, ok := strings.Cut(file.Name(), "_")
		if !ok {
			continue
		}
		version, err := strconv.Atoi(versionText)
		if err != nil {
			return fmt.Errorf("invalid migration %q: %w", file.Name(), err)
		}
		var applied bool
		if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)", version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if applied {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + file.Name())
		if err != nil {
			return fmt.Errorf("read migration %d: %w", version, err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err = tx.ExecContext(ctx, string(body)); err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (?)", version)
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return nil
}

func (s *Store) AddFeed(ctx context.Context, feedURL string) (domain.Feed, error) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return domain.Feed{}, errors.New("feed URL is empty")
	}
	parsedURL, err := url.Parse(feedURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return domain.Feed{}, fmt.Errorf("invalid feed URL %q (use http or https)", feedURL)
	}
	result, err := s.db.ExecContext(ctx, "INSERT INTO feeds(url, title) VALUES (?, ?) ON CONFLICT(url) DO NOTHING", feedURL, feedURL)
	if err != nil {
		return domain.Feed{}, fmt.Errorf("add feed: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return domain.Feed{}, fmt.Errorf("inspect added feed: %w", err)
	}
	var id int64
	if rows == 0 {
		err = s.db.QueryRowContext(ctx, "SELECT id FROM feeds WHERE url = ?", feedURL).Scan(&id)
		if err != nil {
			return domain.Feed{}, fmt.Errorf("find feed: %w", err)
		}
	} else {
		id, err = result.LastInsertId()
		if err != nil {
			return domain.Feed{}, fmt.Errorf("read added feed ID: %w", err)
		}
	}
	return s.Feed(ctx, id)
}

func (s *Store) DeleteFeed(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM feeds WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete feed: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

const feedSelect = `SELECT f.id, f.url, f.title, f.site_url, f.etag, f.last_modified,
 f.last_refreshed, f.last_error,
 COALESCE(SUM(CASE WHEN COALESCE(es.is_read, 0) = 0 AND e.id IS NOT NULL THEN 1 ELSE 0 END), 0)
 FROM feeds f LEFT JOIN entries e ON e.feed_id = f.id
 LEFT JOIN entry_state es ON es.entry_id = e.id`

func (s *Store) Feed(ctx context.Context, id int64) (domain.Feed, error) {
	row := s.db.QueryRowContext(ctx, feedSelect+" WHERE f.id = ? GROUP BY f.id", id)
	feed, err := scanFeed(row)
	if err != nil {
		return domain.Feed{}, err
	}
	return feed, nil
}

func (s *Store) Feeds(ctx context.Context) ([]domain.Feed, error) {
	rows, err := s.db.QueryContext(ctx, feedSelect+" GROUP BY f.id ORDER BY f.title COLLATE NOCASE, f.id")
	if err != nil {
		return nil, fmt.Errorf("list feeds: %w", err)
	}
	defer rows.Close()
	var feeds []domain.Feed
	for rows.Next() {
		feed, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanFeed(row scanner) (domain.Feed, error) {
	var feed domain.Feed
	var refreshed string
	err := row.Scan(&feed.ID, &feed.URL, &feed.Title, &feed.SiteURL, &feed.ETag,
		&feed.LastModified, &refreshed, &feed.LastError, &feed.UnreadCount)
	if err != nil {
		return feed, fmt.Errorf("scan feed: %w", err)
	}
	feed.LastRefreshed = parseTime(refreshed)
	return feed, nil
}

func (s *Store) RecordRefreshError(ctx context.Context, id int64, refreshErr error) error {
	message := ""
	if refreshErr != nil {
		message = refreshErr.Error()
	}
	_, err := s.db.ExecContext(ctx, "UPDATE feeds SET last_error = ?, last_refreshed = ? WHERE id = ?", message, formatTime(time.Now()), id)
	return err
}

// ApplyRefresh serializes feed metadata and entry upserts in one transaction.
func (s *Store) ApplyRefresh(ctx context.Context, feedID int64, parsed domain.ParsedFeed) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin refresh: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE feeds SET
 title = CASE WHEN ? = '' THEN title ELSE ? END,
 site_url = CASE WHEN ? = '' THEN site_url ELSE ? END,
 etag = ?,
 last_modified = ?,
 last_refreshed = ?, last_error = '' WHERE id = ?`,
		parsed.Title, parsed.Title, parsed.SiteURL, parsed.SiteURL,
		parsed.ETag, parsed.LastModified,
		formatTime(time.Now()), feedID)
	if err != nil {
		return 0, fmt.Errorf("update feed: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return 0, sql.ErrNoRows
	}
	if parsed.NotModified {
		return 0, tx.Commit()
	}
	added := 0
	for _, entry := range parsed.Entries {
		result, err := tx.ExecContext(ctx, `INSERT INTO entries
 (feed_id, identity, url, title, author, published_at, updated_at, html, searchable_text)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
 ON CONFLICT(feed_id, identity) DO UPDATE SET
 url=excluded.url, title=excluded.title, author=excluded.author,
 published_at=excluded.published_at, updated_at=excluded.updated_at,
 html=excluded.html, searchable_text=excluded.searchable_text`,
			feedID, entry.Identity, entry.URL, entry.Title, entry.Author,
			formatTime(entry.PublishedAt), formatTime(entry.UpdatedAt), entry.HTML, entry.Text)
		if err != nil {
			return 0, fmt.Errorf("upsert entry %q: %w", entry.Title, err)
		}
		if n, _ := result.RowsAffected(); n > 0 {
			var exists bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM entry_state es JOIN entries e ON e.id=es.entry_id WHERE e.feed_id=? AND e.identity=?)", feedID, entry.Identity).Scan(&exists); err != nil {
				return 0, err
			}
			if !exists {
				var entryID int64
				if err := tx.QueryRowContext(ctx, "SELECT id FROM entries WHERE feed_id=? AND identity=?", feedID, entry.Identity).Scan(&entryID); err != nil {
					return 0, err
				}
				if _, err := tx.ExecContext(ctx, "INSERT INTO entry_state(entry_id) VALUES (?)", entryID); err != nil {
					return 0, err
				}
				added++
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit refresh: %w", err)
	}
	return added, nil
}

func (s *Store) Entries(ctx context.Context, filter domain.EntryFilter) ([]domain.Entry, error) {
	query := `SELECT e.id, e.feed_id, f.title, e.identity, e.url, e.title, e.author,
 e.published_at, e.updated_at, e.html, e.searchable_text,
 COALESCE(es.is_read, 0), COALESCE(es.is_starred, 0)
 FROM entries e JOIN feeds f ON f.id=e.feed_id
 LEFT JOIN entry_state es ON es.entry_id=e.id WHERE 1=1`
	var args []any
	if filter.FeedID != 0 {
		query += " AND e.feed_id=?"
		args = append(args, filter.FeedID)
	}
	if filter.UnreadOnly {
		query += " AND COALESCE(es.is_read, 0)=0"
	}
	if filter.StarredOnly {
		query += " AND COALESCE(es.is_starred, 0)=1"
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		query += " AND (e.title LIKE ? ESCAPE '\\' OR e.searchable_text LIKE ? ESCAPE '\\')"
		pattern := "%" + escapeLike(search) + "%"
		args = append(args, pattern, pattern)
	}
	query += " ORDER BY CASE WHEN e.published_at='' THEN e.updated_at ELSE e.published_at END DESC, e.id DESC"
	limit := filter.Limit
	if limit <= 0 {
		limit = 1000
	}
	query += " LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()
	var entries []domain.Entry
	for rows.Next() {
		var entry domain.Entry
		var published, updated string
		if err := rows.Scan(&entry.ID, &entry.FeedID, &entry.FeedTitle, &entry.Identity,
			&entry.URL, &entry.Title, &entry.Author, &published, &updated, &entry.HTML,
			&entry.Text, &entry.Read, &entry.Starred); err != nil {
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		entry.PublishedAt, entry.UpdatedAt = parseTime(published), parseTime(updated)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) SetRead(ctx context.Context, id int64, read bool) error {
	return s.setState(ctx, id, "is_read", read)
}

func (s *Store) SetStarred(ctx context.Context, id int64, starred bool) error {
	return s.setState(ctx, id, "is_starred", starred)
}

func (s *Store) setState(ctx context.Context, id int64, column string, value bool) error {
	// column is internal and deliberately not caller-controlled.
	result, err := s.db.ExecContext(ctx, `INSERT INTO entry_state(entry_id, `+column+`)
 VALUES (?, ?) ON CONFLICT(entry_id) DO UPDATE SET `+column+`=excluded.`+column, id, value)
	if err != nil {
		return fmt.Errorf("update entry state: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}
