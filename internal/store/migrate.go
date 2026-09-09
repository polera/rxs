package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/polera/rxs/internal/article"
	"github.com/polera/rxs/internal/domain"
)

// Bump whenever article.Candidate or InputHash semantics change. Open recomputes
// derived metadata transactionally before serving even a 304 refresh. Attempts
// and successful overlays are retained; a policy change alone is not a retry.
const enrichmentPolicyVersion = 1

// SQL's strftime only retains milliseconds. Normalize in bounded Go batches to
// preserve the instants (and hence article.InputHash) of legacy RFC3339Nano dates.
func normalizeStoredDates(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []struct {
		name, id string
		columns  []string
	}{
		{"entries", "id", []string{"published_at", "updated_at"}},
		{"feeds", "id", []string{"last_refreshed", "created_at"}},
		{"entry_content", "entry_id", []string{"attempted_at", "fetched_at"}},
	} {
		assignments := make([]string, len(table.columns))
		for i, column := range table.columns {
			assignments[i] = column + "=?"
		}
		// #nosec G202 -- Identifiers come only from the fixed migration table list above.
		stmt, err := tx.PrepareContext(ctx, "UPDATE "+table.name+" SET "+strings.Join(assignments, ",")+" WHERE "+table.id+"=?")
		if err != nil {
			return err
		}
		defer stmt.Close()
		var lastID int64
		for {
			// #nosec G202 -- Only fixed migration identifiers are concatenated; values are bound.
			rows, err := tx.QueryContext(ctx, "SELECT "+table.id+","+strings.Join(table.columns, ",")+" FROM "+table.name+" WHERE "+table.id+">? ORDER BY "+table.id+" LIMIT 256", lastID)
			if err != nil {
				return err
			}
			var batch [][]any
			for rows.Next() {
				values := make([]string, len(table.columns))
				dest := []any{&lastID}
				for i := range values {
					dest = append(dest, &values[i])
				}
				if err := rows.Scan(dest...); err != nil {
					_ = rows.Close()
					return err
				}
				args := make([]any, 0, len(values)+1)
				for i, value := range values {
					if value != "" {
						parsed, err := time.Parse(time.RFC3339Nano, value)
						if err != nil {
							// Shipped feeds.created_at used SQLite CURRENT_TIMESTAMP.
							parsed, err = time.Parse("2006-01-02 15:04:05", value)
						}
						if err != nil {
							_ = rows.Close()
							return fmt.Errorf("normalize %s.%s row %d: %w", table.name, table.columns[i], lastID, err)
						}
						value = formatTime(parsed)
					}
					args = append(args, value)
				}
				batch = append(batch, append(args, lastID))
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if len(batch) == 0 {
				break
			}
			for _, args := range batch {
				if _, err := stmt.ExecContext(ctx, args...); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Store) backfillEnrichment(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin enrichment backfill: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT value FROM store_metadata WHERE key='enrichment_policy'").Scan(&version); err != nil {
		return err
	}
	if version == enrichmentPolicyVersion {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, "UPDATE entries SET enrichment_input_hash=?, enrichment_eligible=? WHERE id=?")
	if err != nil {
		return err
	}
	defer stmt.Close()
	var lastID int64
	for {
		rows, err := tx.QueryContext(ctx, `SELECT id, url, html, searchable_text, updated_at FROM entries WHERE id>? ORDER BY id LIMIT 256`, lastID)
		if err != nil {
			return err
		}
		var batch []domain.Entry
		for rows.Next() {
			var entry domain.Entry
			var updated string
			if err := rows.Scan(&entry.ID, &entry.URL, &entry.HTML, &entry.Text, &updated); err != nil {
				_ = rows.Close()
				return err
			}
			entry.UpdatedAt = parseTime(updated)
			batch = append(batch, entry)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, entry := range batch {
			if _, err := stmt.ExecContext(ctx, article.InputHash(entry.URL, entry.HTML, entry.UpdatedAt), article.Candidate(entry), entry.ID); err != nil {
				return err
			}
			lastID = entry.ID
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE store_metadata SET value=? WHERE key='enrichment_policy'", enrichmentPolicyVersion); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit enrichment backfill: %w", err)
	}
	return nil
}
