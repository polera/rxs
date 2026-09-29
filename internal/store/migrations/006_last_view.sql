CREATE TABLE last_view (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    pane TEXT NOT NULL,
    scope TEXT NOT NULL,
    feed_id INTEGER NOT NULL DEFAULT 0,
    feed_url TEXT NOT NULL DEFAULT '',
    entry_id INTEGER NOT NULL DEFAULT 0,
    entry_feed_url TEXT NOT NULL DEFAULT '',
    entry_identity TEXT NOT NULL DEFAULT ''
);
