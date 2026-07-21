package feed

import (
	"context"
	"fmt"
	"sync"

	"github.com/polera/rxs/internal/domain"
)

type Repository interface {
	Feed(context.Context, int64) (domain.Feed, error)
	ApplyRefresh(context.Context, int64, domain.ParsedFeed) (int, error)
	RecordRefreshError(context.Context, int64, error) error
}

type Fetcher interface {
	Fetch(context.Context, domain.Feed) (domain.ParsedFeed, error)
}

type Service struct {
	repository Repository
	client     Fetcher
}

func NewService(repository Repository, client Fetcher) *Service {
	return &Service{repository: repository, client: client}
}

func (s *Service) Refresh(ctx context.Context, id int64) domain.RefreshResult {
	source, err := s.repository.Feed(ctx, id)
	if err != nil {
		return domain.RefreshResult{FeedID: id, Err: fmt.Errorf("load feed: %w", err)}
	}
	result := domain.RefreshResult{FeedID: id, Title: source.Title}
	parsed, err := s.client.Fetch(ctx, source)
	if err == nil {
		result.Added, err = s.repository.ApplyRefresh(ctx, id, parsed)
	}
	if err != nil {
		_ = s.repository.RecordRefreshError(ctx, id, err)
		result.Err = err
	}
	return result
}

// RefreshAll uses a bounded worker pool. Store writes remain serialized by the
// repository's single SQLite connection while network requests run concurrently.
func (s *Service) RefreshAll(ctx context.Context, feeds []domain.Feed, workers int) []domain.RefreshResult {
	if workers < 1 {
		workers = 1
	}
	if workers > len(feeds) {
		workers = len(feeds)
	}
	if workers == 0 {
		return nil
	}
	jobs := make(chan int64)
	results := make(chan domain.RefreshResult, len(feeds))
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for id := range jobs {
				results <- s.Refresh(ctx, id)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, source := range feeds {
			select {
			case jobs <- source.ID:
			case <-ctx.Done():
				return
			}
		}
	}()
	group.Wait()
	close(results)
	all := make([]domain.RefreshResult, 0, len(feeds))
	for result := range results {
		all = append(all, result)
	}
	return all
}
