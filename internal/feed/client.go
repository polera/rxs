// Package feed downloads and normalizes RSS, Atom, and JSON feeds.
package feed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/render"
)

const defaultMaxBytes int64 = 10 << 20

type Client struct {
	http      *http.Client
	maxBytes  int64
	userAgent string
}

func NewClient(version string) *Client {
	if version == "" {
		version = "dev"
	}
	return &Client{
		http: &http.Client{
			Timeout: 20 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("stopped after 5 redirects")
				}
				return nil
			},
		},
		maxBytes:  defaultMaxBytes,
		userAgent: "rxs/" + version + " (+https://github.com/polera/rxs)",
	}
}

func (c *Client) Fetch(ctx context.Context, source domain.Feed) (domain.ParsedFeed, error) {
	parsedURL, err := url.Parse(source.URL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return domain.ParsedFeed{}, fmt.Errorf("invalid feed URL %q (use http or https)", source.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return domain.ParsedFeed{}, fmt.Errorf("create feed request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/feed+json, application/json, application/xml, text/xml;q=0.9, */*;q=0.1")
	if source.ETag != "" {
		req.Header.Set("If-None-Match", source.ETag)
	}
	if source.LastModified != "" {
		req.Header.Set("If-Modified-Since", source.LastModified)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return domain.ParsedFeed{}, fmt.Errorf("fetch %s: %w", source.URL, err)
	}
	defer response.Body.Close()
	result := domain.ParsedFeed{
		ETag:         response.Header.Get("ETag"),
		LastModified: response.Header.Get("Last-Modified"),
	}
	if response.StatusCode == http.StatusNotModified {
		if result.ETag == "" {
			result.ETag = source.ETag
		}
		if result.LastModified == "" {
			result.LastModified = source.LastModified
		}
		result.NotModified = true
		return result, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("fetch %s: server returned %s", source.URL, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, c.maxBytes+1))
	if err != nil {
		return result, fmt.Errorf("read %s: %w", source.URL, err)
	}
	if int64(len(body)) > c.maxBytes {
		return result, fmt.Errorf("feed exceeds %d MiB response limit", c.maxBytes/(1<<20))
	}
	// A parser is cheap and kept request-local so RefreshAll can parse safely
	// across its worker pool without relying on undocumented shared state.
	parsed, err := gofeed.NewParser().ParseString(string(body))
	if err != nil {
		return result, fmt.Errorf("parse %s: %w", source.URL, err)
	}
	result.Title = strings.TrimSpace(parsed.Title)
	result.SiteURL = strings.TrimSpace(parsed.Link)
	result.Entries = make([]domain.Entry, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		content := item.Content
		if strings.TrimSpace(content) == "" {
			content = item.Description
		}
		entry := domain.Entry{
			Identity: identity(item),
			URL:      strings.TrimSpace(item.Link),
			Title:    strings.TrimSpace(item.Title),
			Author:   itemAuthor(item),
			HTML:     content,
			Text:     render.Text(content),
		}
		if entry.Title == "" {
			entry.Title = entry.URL
		}
		if entry.Title == "" {
			entry.Title = "Untitled"
		}
		if item.PublishedParsed != nil {
			entry.PublishedAt = *item.PublishedParsed
		}
		if item.UpdatedParsed != nil {
			entry.UpdatedAt = *item.UpdatedParsed
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, nil
}

func identity(item *gofeed.Item) string {
	if id := strings.TrimSpace(item.GUID); id != "" {
		return "guid:" + id
	}
	if link := strings.TrimSpace(item.Link); link != "" {
		return "url:" + link
	}
	value := strings.TrimSpace(item.Title) + "\x00" + strings.TrimSpace(item.Published)
	if value == "\x00" {
		value = item.Description + "\x00" + item.Content
	}
	hash := sha256.Sum256([]byte(value))
	return "hash:" + hex.EncodeToString(hash[:])
}

func itemAuthor(item *gofeed.Item) string {
	if item.Author != nil {
		if name := strings.TrimSpace(item.Author.Name); name != "" {
			return name
		}
		return strings.TrimSpace(item.Author.Email)
	}
	if len(item.Authors) > 0 && item.Authors[0] != nil {
		return strings.TrimSpace(item.Authors[0].Name)
	}
	return ""
}
