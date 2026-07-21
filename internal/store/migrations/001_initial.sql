CREATE TABLE feeds (
    id INTEGER PRIMARY KEY,
    url TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    site_url TEXT NOT NULL DEFAULT '',
    etag TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    last_refreshed TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE entries (
    id INTEGER PRIMARY KEY,
    feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    identity TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    published_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    html TEXT NOT NULL DEFAULT '',
    searchable_text TEXT NOT NULL DEFAULT '',
    UNIQUE(feed_id, identity)
);

CREATE INDEX entries_feed_date ON entries(feed_id, published_at DESC, id DESC);

CREATE TABLE entry_state (
    entry_id INTEGER PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    is_read INTEGER NOT NULL DEFAULT 0,
    is_starred INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
