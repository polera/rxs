package feed

import (
	"context"
	"fmt"
	"sync"

	"github.com/polera/rxs/internal/article"
	"github.com/polera/rxs/internal/domain"
)

const maxEnrichmentsPerRefresh = 10

type Repository interface {
	Feed(context.Context, int64) (domain.Feed, error)
	ApplyRefresh(context.Context, int64, domain.ParsedFeed) (int, error)
	RecordRefreshError(context.Context, int64, error) error
}

type enrichmentRepository interface {
	EnrichmentCandidates(context.Context, int64, int) ([]domain.Entry, error)
	SaveEnrichment(context.Context, int64, string, article.Content) error
	RecordEnrichmentError(context.Context, int64, string, error) error
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
		inputHash := entry.EnrichmentInputHash
		if inputHash == "" {
			inputHash = article.InputHash(entry.URL, entry.HTML, entry.UpdatedAt)
		}
		content, extractErr := s.extractor.Extract(ctx, entry.URL)
		if extractErr == nil {
			extractErr = article.Validate(entry, content)
		}
		if extractErr != nil {
			if ctx.Err() != nil {
				return
			}
			_ = repository.RecordEnrichmentError(ctx, entry.ID, inputHash, extractErr)
			result.ExpansionFailed++
			continue
		}
		if err := repository.SaveEnrichment(ctx, entry.ID, inputHash, content); err != nil {
			_ = repository.RecordEnrichmentError(ctx, entry.ID, inputHash, err)
			result.ExpansionFailed++
			continue
		}
		result.Expanded++
	}
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
