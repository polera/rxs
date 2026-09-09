-- The migration runner first normalizes legacy timestamps with Go's time parser
-- in this transaction: SQLite date functions would lose nanosecond precision.
CREATE INDEX entries_feed_effective_date ON entries
    (feed_id, (CASE WHEN published_at='' THEN updated_at ELSE published_at END) DESC, id DESC);
CREATE INDEX entries_effective_date ON entries
    ((CASE WHEN published_at='' THEN updated_at ELSE published_at END) DESC, id DESC);
DROP INDEX entries_feed_date;
