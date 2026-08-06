package domain

import "time"

// Feed is a persisted subscription and its HTTP cache metadata.
type Feed struct {
	ID            int64
	URL           string
	Title         string
	SiteURL       string
	ETag          string
	LastModified  string
	LastRefreshed time.Time
	LastError     string
	UnreadCount   int
}

// Entry is an item downloaded from a feed. User-owned state is intentionally
// separate from fetched metadata in the database so refreshes cannot erase it.
type Entry struct {
	ID          int64
	FeedID      int64
	FeedTitle   string
	Identity    string
	URL         string
	Title       string
	Author      string
	PublishedAt time.Time
	UpdatedAt   time.Time
	HTML        string
	Text        string
	// ContentSource identifies whether HTML and Text came from the feed or a
	// separately downloaded full-article overlay.
	ContentSource string
	// EnrichmentInputHash is populated only for pending enrichment work.
	EnrichmentInputHash string
	Read                bool
	Starred             bool
	// ReadingProgress is the last saved position in the article, from 0 to 1.
	ReadingProgress float64
}

// EntryFilter describes the local article list. FeedID zero means all feeds.
type EntryFilter struct {
	FeedID      int64
	UnreadOnly  bool
	StarredOnly bool
	Search      string
	Limit       int
}

// ParsedFeed is normalized feed content ready to store atomically.
type ParsedFeed struct {
	Title        string
	SiteURL      string
	ETag         string
	LastModified string
	NotModified  bool
	Entries      []Entry
}

// RefreshResult is delivered to the UI after one refresh attempt.
type RefreshResult struct {
	FeedID          int64
	Title           string
	Added           int
	Expanded        int
	ExpansionFailed int
	Err             error
}

const (
	ContentSourceFeed        = "feed"
	ContentSourceFullArticle = "full_article"
)
