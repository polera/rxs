package feed

import (
	"context"
	"fmt"
	"sync"

	"github.com/polera/rxs/internal/article"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/store"
)

const maxEnrichmentsPerRefresh = 10

type Repository interface {
	Feed(context.Context, int64) (domain.Feed, error)
	ApplyRefresh(context.Context, int64, domain.ParsedFeed) (int, error)
	RecordRefreshError(context.Context, int64, error) error
}

type enrichmentRepository interface {
	EnrichmentCandidates(context.Context, int64, int) ([]store.EnrichmentCandidate, error)
	SaveEnrichment(context.Context, store.EnrichmentCandidate, article.Content) (bool, error)
	RecordEnrichmentError(context.Context, store.EnrichmentCandidate, error) (bool, error)
}

type Fetcher interface {
	Fetch(context.Context, domain.Feed) (domain.ParsedFeed, error)
}

type Service struct {
	repository Repository
	client     Fetcher
	extractor  article.Extractor
}

type Option func(*Service)

// WithArticleExtractor enables conservative full-article enrichment. Omitting
// this option keeps enrichment off and performs no article-page requests.
func WithArticleExtractor(extractor article.Extractor) Option {
	return func(service *Service) { service.extractor = extractor }
}

func NewService(repository Repository, client Fetcher, options ...Option) *Service {
	service := &Service{repository: repository, client: client}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Refresh(ctx context.Context, id int64) domain.RefreshResult {
	if err := ctx.Err(); err != nil {
		return domain.RefreshResult{FeedID: id, Err: err}
	}
	source, err := s.repository.Feed(ctx, id)
	if err != nil {
		return domain.RefreshResult{FeedID: id, Err: fmt.Errorf("load feed: %w", err)}
	}
	result := domain.RefreshResult{FeedID: id, Title: source.Title}
	if ctx.Err() != nil {
		result.Err = ctx.Err()
		return result
	}
	parsed, err := s.client.Fetch(ctx, source)
	if ctx.Err() != nil {
		result.Err = ctx.Err()
		return result
	}
	if err == nil {
		result.Added, err = s.repository.ApplyRefresh(ctx, id, parsed)
	}
	if err != nil {
		if ctx.Err() == nil {
			_ = s.repository.RecordRefreshError(ctx, id, err)
		}
		result.Err = err
		return result
	}
	if s.extractor != nil {
		s.enrich(ctx, id, &result)
	}
	return result
}

func (s *Service) enrich(ctx context.Context, feedID int64, result *domain.RefreshResult) {
	repository, ok := s.repository.(enrichmentRepository)
	if !ok {
		result.ExpansionFailed++
		return
	}
	candidates, err := repository.EnrichmentCandidates(ctx, feedID, maxEnrichmentsPerRefresh)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		result.ExpansionFailed++
		return
	}
	for _, entry := range candidates {
		if ctx.Err() != nil {
			return
		}
		content, extractErr := s.extractor.Extract(ctx, entry.URL)
		if ctx.Err() != nil {
			return
		}
		if extractErr == nil {
			extractErr = article.Validate(entry.Entry, content)
		}
		if extractErr != nil {
			if ctx.Err() != nil {
				return
			}
			if applied, err := repository.RecordEnrichmentError(ctx, entry, extractErr); applied || err != nil {
				result.ExpansionFailed++
			}
			continue
		}
		if applied, err := repository.SaveEnrichment(ctx, entry, content); err != nil {
			result.ExpansionFailed++
		} else if applied {
			result.Expanded++
		}
	}
}

// RefreshAll uses a bounded worker pool. Store writes remain serialized by the
// repository's single SQLite connection while network requests run concurrently.
// Cancellation stops admission and joins workers and the producer. The returned
// slice contains only attempted feeds, not placeholders for canceled queued jobs.
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
				if ctx.Err() != nil {
					return
				}
				results <- s.Refresh(ctx, id)
			}
		}()
	}
	group.Add(1)
	go func() {
		defer group.Done()
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
