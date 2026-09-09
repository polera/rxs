package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/polera/rxs/internal/domain"
)

var benchmarkView string

func benchmarkReaderModel() Model {
	m := New(nil, nil, nil)
	m.width, m.height = 120, 30
	m.active = articlesPane
	var html strings.Builder
	for i := range 200 {
		fmt.Fprintf(&html, `<p>Paragraph %d: a long article with searchable needle words and <a href="/link/%d">linked needle text %d</a> followed by more prose for wrapping.</p>`, i, i, i)
	}
	m.entries = []domain.Entry{{ID: 1, Title: "Long link-heavy article", FeedTitle: "Benchmark", URL: "https://example.test/article", HTML: html.String()}}
	m.resizeReader()
	m.syncReader()
	return m
}

func BenchmarkReader(b *testing.B) {
	for _, action := range []string{"Opening", "Search", "Tab", "Boundary"} {
		b.Run(action, func(b *testing.B) {
			m := benchmarkReaderModel()
			if action == "Search" || action == "Tab" {
				next, _ := m.enterReader()
				m = next.(Model)
			}
			if action == "Search" {
				m.searchReader("needle")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				switch action {
				case "Opening":
					next, _ := m.enterReader()
					benchmarkView = next.(Model).reader.GetContent()
				case "Search":
					m.selectReaderMatch(1)
				case "Tab":
					m.selectReaderLink(1)
				case "Boundary":
					m.move(1)
					m.moveToListBoundary(true)
				}
			}
		})
	}
}

func BenchmarkVisibleRows(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		for _, pane := range []string{"Articles", "Feeds"} {
			b.Run(fmt.Sprintf("%s/%d", pane, count), func(b *testing.B) {
				m := New(nil, nil, nil)
				m.width, m.height = 120, 30
				date := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
				for i := range count {
					m.entries = append(m.entries, domain.Entry{ID: int64(i + 1), Title: fmt.Sprintf("Article %d with a reasonably long title", i), FeedTitle: "Example feed", PublishedAt: date})
					feed := domain.Feed{ID: int64(i + 1), Title: fmt.Sprintf("Feed %d", i), UnreadCount: i % 10}
					if i%5 == 0 {
						feed.LastError = "fetch failed: service unavailable"
					}
					m.feeds = append(m.feeds, feed)
				}
				m.allFeeds = m.feeds
				m.entryCursor, m.feedCursor = count-1, count+1
				m.active = articlesPane
				if pane == "Feeds" {
					m.active = feedsPane
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if pane == "Articles" {
						benchmarkView = m.entriesView(36)
					} else {
						benchmarkView = m.feedsView(26)
					}
				}
			})
		}
	}
}
