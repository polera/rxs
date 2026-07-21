package feed

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polera/rxs/internal/domain"
)

type repositoryStub struct {
	mu      sync.Mutex
	feeds   map[int64]domain.Feed
	applied map[int64]int
	errors  map[int64]error
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
