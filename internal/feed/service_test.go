package feed

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polera/rxs/internal/article"
	"github.com/polera/rxs/internal/domain"
)

type repositoryStub struct {
	mu      sync.Mutex
	feeds   map[int64]domain.Feed
	applied map[int64]int
	errors  map[int64]error
}

type staticFetcher struct {
	parsed domain.ParsedFeed
	err    error
}

func (f staticFetcher) Fetch(context.Context, domain.Feed) (domain.ParsedFeed, error) {
	return f.parsed, f.err
}

type enrichmentRepositoryStub struct {
	*repositoryStub
	candidates       []domain.Entry
	saved            []int64
	enrichmentErrors []int64
}

func (r *enrichmentRepositoryStub) EnrichmentCandidates(_ context.Context, _ int64, limit int) ([]domain.Entry, error) {
	if len(r.candidates) > limit {
		return r.candidates[:limit], nil
	}
	return r.candidates, nil
}

func (r *enrichmentRepositoryStub) SaveEnrichment(_ context.Context, entryID int64, _ string, _ article.Content) error {
	r.saved = append(r.saved, entryID)
	return nil
}

func (r *enrichmentRepositoryStub) RecordEnrichmentError(_ context.Context, entryID int64, _ string, _ error) error {
	r.enrichmentErrors = append(r.enrichmentErrors, entryID)
	return nil
}

type extractorStub struct {
	content article.Content
	err     error
	calls   int
}

type concurrentEnrichmentRepository struct{ *repositoryStub }

func (r *concurrentEnrichmentRepository) EnrichmentCandidates(_ context.Context, feedID int64, _ int) ([]domain.Entry, error) {
	return []domain.Entry{{
		ID: feedID, URL: "https://example.test/article", Title: "Matching Article",
		Text: "short summary", EnrichmentInputHash: "hash",
	}}, nil
}

func (r *concurrentEnrichmentRepository) SaveEnrichment(context.Context, int64, string, article.Content) error {
	return nil
}

func (r *concurrentEnrichmentRepository) RecordEnrichmentError(context.Context, int64, string, error) error {
	return nil
}

type concurrentExtractor struct {
	active atomic.Int32
	max    atomic.Int32
}

func (e *concurrentExtractor) Extract(context.Context, string) (article.Content, error) {
	active := e.active.Add(1)
	for {
		old := e.max.Load()
		if active <= old || e.max.CompareAndSwap(old, active) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	e.active.Add(-1)
	return article.Content{Title: "Matching Article", Text: strings.Repeat("Full matching article body. ", 40)}, nil
}

func (e *extractorStub) Extract(context.Context, string) (article.Content, error) {
	e.calls++
	return e.content, e.err
}

func (r *repositoryStub) Feed(_ context.Context, id int64) (domain.Feed, error) {
	return r.feeds[id], nil
}
func (r *repositoryStub) ApplyRefresh(_ context.Context, id int64, parsed domain.ParsedFeed) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applied[id]++
	return len(parsed.Entries), nil
}
func (r *repositoryStub) RecordRefreshError(_ context.Context, id int64, err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors[id] = err
	return nil
}

type concurrentFetcher struct {
	active atomic.Int32
	max    atomic.Int32
}

func (f *concurrentFetcher) Fetch(context.Context, domain.Feed) (domain.ParsedFeed, error) {
	active := f.active.Add(1)
	for {
		old := f.max.Load()
		if active <= old || f.max.CompareAndSwap(old, active) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	f.active.Add(-1)
	return domain.ParsedFeed{Entries: []domain.Entry{{Identity: "one"}}}, nil
}

func TestRefreshAllBoundsWorkers(t *testing.T) {
	repository := &repositoryStub{
		feeds: make(map[int64]domain.Feed), applied: make(map[int64]int), errors: make(map[int64]error),
	}
	var feeds []domain.Feed
	for id := int64(1); id <= 8; id++ {
		source := domain.Feed{ID: id, URL: "https://example.test/feed"}
		feeds = append(feeds, source)
		repository.feeds[id] = source
	}
	fetcher := &concurrentFetcher{}
	results := NewService(repository, fetcher).RefreshAll(context.Background(), feeds, 3)
	if len(results) != len(feeds) || len(repository.applied) != len(feeds) {
		t.Fatalf("results=%d applied=%d", len(results), len(repository.applied))
	}
	if maximum := fetcher.max.Load(); maximum < 2 || maximum > 3 {
		t.Fatalf("maximum concurrent fetches = %d, want 2..3", maximum)
	}
}

func TestRefreshBackfillsNotModifiedFeedAndLimitsExpansions(t *testing.T) {
	base := &repositoryStub{
		feeds:   map[int64]domain.Feed{1: {ID: 1, URL: "https://example.test/feed"}},
		applied: make(map[int64]int), errors: make(map[int64]error),
	}
	repository := &enrichmentRepositoryStub{repositoryStub: base}
	for id := int64(1); id <= 12; id++ {
		repository.candidates = append(repository.candidates, domain.Entry{
			ID: id, URL: "https://example.test/article", Title: "Matching Article",
			Text: "short summary", EnrichmentInputHash: "hash",
		})
	}
	extractor := &extractorStub{content: article.Content{
		Title: "Matching Article", Text: strings.Repeat("Full matching article body. ", 40),
	}}
	result := NewService(repository, staticFetcher{parsed: domain.ParsedFeed{NotModified: true}},
		WithArticleExtractor(extractor)).Refresh(context.Background(), 1)
	if result.Err != nil || result.Expanded != 10 || result.ExpansionFailed != 0 {
		t.Fatalf("refresh result = %#v", result)
	}
	if extractor.calls != 10 || len(repository.saved) != 10 || base.applied[1] != 1 {
		t.Fatalf("calls=%d saved=%d applied=%d", extractor.calls, len(repository.saved), base.applied[1])
	}
}

func TestExpansionFailureDoesNotFailFeedRefresh(t *testing.T) {
	base := &repositoryStub{
		feeds:   map[int64]domain.Feed{1: {ID: 1, URL: "https://example.test/feed"}},
		applied: make(map[int64]int), errors: make(map[int64]error),
	}
	repository := &enrichmentRepositoryStub{
		repositoryStub: base,
		candidates: []domain.Entry{{
			ID: 1, URL: "https://example.test/article", Title: "Article", EnrichmentInputHash: "hash",
		}},
	}
	extractor := &extractorStub{err: errors.New("page unavailable")}
	result := NewService(repository, staticFetcher{}, WithArticleExtractor(extractor)).Refresh(context.Background(), 1)
	if result.Err != nil || result.Expanded != 0 || result.ExpansionFailed != 1 {
		t.Fatalf("refresh result = %#v", result)
	}
	if len(base.errors) != 0 || len(repository.enrichmentErrors) != 1 {
		t.Fatalf("feed errors=%v enrichment errors=%v", base.errors, repository.enrichmentErrors)
	}
}

func TestRefreshAllBoundsArticleRequestsToFeedWorkers(t *testing.T) {
	base := &repositoryStub{
		feeds: make(map[int64]domain.Feed), applied: make(map[int64]int), errors: make(map[int64]error),
	}
	var feeds []domain.Feed
	for id := int64(1); id <= 8; id++ {
		source := domain.Feed{ID: id, URL: "https://example.test/feed"}
		feeds = append(feeds, source)
		base.feeds[id] = source
	}
	repository := &concurrentEnrichmentRepository{repositoryStub: base}
	extractor := &concurrentExtractor{}
	results := NewService(repository, staticFetcher{}, WithArticleExtractor(extractor)).RefreshAll(context.Background(), feeds, 3)
	if len(results) != len(feeds) {
		t.Fatalf("results = %d, want %d", len(results), len(feeds))
	}
	for _, result := range results {
		if result.Err != nil || result.Expanded != 1 {
			t.Fatalf("refresh result = %#v", result)
		}
	}
	if maximum := extractor.max.Load(); maximum < 2 || maximum > 3 {
		t.Fatalf("maximum concurrent article requests = %d, want 2..3", maximum)
	}
}
