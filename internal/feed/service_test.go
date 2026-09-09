package feed

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polera/rxs/internal/article"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/store"
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
	candidates       []store.EnrichmentCandidate
	saved            []int64
	enrichmentErrors []int64
}

func (r *enrichmentRepositoryStub) EnrichmentCandidates(_ context.Context, _ int64, limit int) ([]store.EnrichmentCandidate, error) {
	if len(r.candidates) > limit {
		return r.candidates[:limit], nil
	}
	return r.candidates, nil
}

func (r *enrichmentRepositoryStub) SaveEnrichment(_ context.Context, candidate store.EnrichmentCandidate, _ article.Content) (bool, error) {
	r.saved = append(r.saved, candidate.ID)
	return true, nil
}

func (r *enrichmentRepositoryStub) RecordEnrichmentError(_ context.Context, candidate store.EnrichmentCandidate, _ error) (bool, error) {
	r.enrichmentErrors = append(r.enrichmentErrors, candidate.ID)
	return true, nil
}

type extractorStub struct {
	content article.Content
	err     error
	calls   int
}

type concurrentEnrichmentRepository struct{ *repositoryStub }

func (r *concurrentEnrichmentRepository) EnrichmentCandidates(_ context.Context, feedID int64, _ int) ([]store.EnrichmentCandidate, error) {
	return []store.EnrichmentCandidate{{Entry: domain.Entry{
		ID: feedID, URL: "https://example.test/article", Title: "Matching Article",
		Text: "short summary", EnrichmentInputHash: "hash",
	}}}, nil
}

func (r *concurrentEnrichmentRepository) SaveEnrichment(context.Context, store.EnrichmentCandidate, article.Content) (bool, error) {
	return true, nil
}

func (r *concurrentEnrichmentRepository) RecordEnrichmentError(context.Context, store.EnrichmentCandidate, error) (bool, error) {
	return true, nil
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
		repository.candidates = append(repository.candidates, store.EnrichmentCandidate{Entry: domain.Entry{
			ID: id, URL: "https://example.test/article", Title: "Matching Article",
			Text: "short summary", EnrichmentInputHash: "hash",
		}})
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
		candidates: []store.EnrichmentCandidate{{Entry: domain.Entry{
			ID: 1, URL: "https://example.test/article", Title: "Article", EnrichmentInputHash: "hash",
		}}},
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

type blockingExtractor struct {
	started chan struct{}
	release chan struct{}
	content article.Content
	err     error
}

func (e *blockingExtractor) Extract(ctx context.Context, _ string) (article.Content, error) {
	close(e.started)
	select {
	case <-e.release:
		return e.content, e.err
	case <-ctx.Done():
		return article.Content{}, ctx.Err()
	}
}

func TestRefreshSkipsObsoleteEnrichmentCompletions(t *testing.T) {
	for _, scenario := range []string{"changed input", "newer success", "same input success", "deleted", "readded feed", "readded entry", "readded identical"} {
		for _, completion := range []string{"success", "failure", "invalid article"} {
			t.Run(scenario+"/"+completion, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				db, err := store.Open(":memory:")
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				source, err := db.AddFeed(ctx, "https://example.test/feed")
				if err != nil {
					t.Fatal(err)
				}
				parsed := domain.ParsedFeed{Entries: []domain.Entry{{
					Identity: "one", URL: "https://example.test/article", Title: "Matching Article",
					HTML: "<p>Short summary</p>", Text: "Short summary",
				}}}
				if _, err := db.ApplyRefresh(ctx, source.ID, parsed); err != nil {
					t.Fatal(err)
				}
				original, err := db.Entries(ctx, domain.EntryFilter{})
				if err != nil || len(original) != 1 {
					t.Fatalf("original entries=%v err=%v", original, err)
				}
				extractor := &blockingExtractor{
					started: make(chan struct{}), release: make(chan struct{}),
					content: article.Content{Title: "Matching Article", Text: strings.Repeat("Old matching article body. ", 40)},
				}
				if completion == "failure" {
					extractor.err = errors.New("old extraction failed")
				} else if completion == "invalid article" {
					extractor.content.Text = "Too short"
				}
				fetcher := staticFetcher{parsed: domain.ParsedFeed{NotModified: true}}
				results := make(chan domain.RefreshResult, 1)
				go func() {
					results <- NewService(db, fetcher, WithArticleExtractor(extractor)).Refresh(ctx, source.ID)
				}()
				// Ensure failed assertions also unblock and join the refresh before closing the store.
				defer func() {
					cancel()
					if results != nil {
						<-results
					}
				}()
				select {
				case <-extractor.started:
				case <-ctx.Done():
					t.Fatal("extractor did not start")
				}
				switch scenario {
				case "changed input", "newer success":
					parsed.Entries[0].HTML = "<p>Changed short summary</p>"
					parsed.Entries[0].Text = "Changed short summary"
					if _, err := db.ApplyRefresh(ctx, source.ID, parsed); err != nil {
						t.Fatal(err)
					}
				case "deleted", "readded feed", "readded entry", "readded identical":
					if err := db.DeleteFeed(ctx, source.ID); err != nil {
						t.Fatal(err)
					}
					if scenario != "deleted" {
						feedURL := source.URL
						if scenario == "readded feed" {
							feedURL = "https://other.example/feed"
						} else if scenario == "readded entry" {
							parsed.Entries[0].Identity = "two"
						}
						replacement, err := db.AddFeed(ctx, feedURL)
						if err != nil || replacement.ID != source.ID {
							t.Fatalf("feed rowid not reused: replacement=%v err=%v", replacement, err)
						}
						if _, err := db.ApplyRefresh(ctx, replacement.ID, parsed); err != nil {
							t.Fatal(err)
						}
					}
				}
				newer := scenario == "newer success" || scenario == "same input success"
				if newer {
					content := article.Content{Title: "Matching Article", Text: strings.Repeat("New matching article body. ", 40)}
					content.HTML = "<p>" + content.Text + "</p>"
					result := NewService(db, fetcher, WithArticleExtractor(&extractorStub{content: content})).Refresh(ctx, source.ID)
					if result.Err != nil || result.Expanded != 1 || result.ExpansionFailed != 0 {
						t.Fatalf("newer refresh=%#v", result)
					}
				}
				before, err := db.Entries(ctx, domain.EntryFilter{})
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(scenario, "readded") && (len(before) != 1 || before[0].ID != original[0].ID) {
					t.Fatalf("entry rowid not reused: before=%v original=%v", before, original)
				}
				close(extractor.release)
				result := <-results
				results = nil
				if result.Err != nil || result.Expanded != 0 || result.ExpansionFailed != 0 {
					t.Fatalf("obsolete refresh=%#v", result)
				}
				after, err := db.Entries(ctx, domain.EntryFilter{})
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("obsolete result changed entries: before=%v after=%v err=%v", before, after, err)
				}
				pending, err := db.EnrichmentCandidates(ctx, source.ID, 10)
				wantPending := 1
				if newer || scenario == "deleted" {
					wantPending = 0
				}
				if err != nil || len(pending) != wantPending {
					t.Fatalf("pending=%v err=%v, want %d candidates", pending, err, wantPending)
				}
			})
		}
	}
}
