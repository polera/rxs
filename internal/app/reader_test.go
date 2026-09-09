package app

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/ui"
)

func TestReaderCacheInvalidatesWhenRelativeDateChanges(t *testing.T) {
	date := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	for _, source := range []string{"published", "updated fallback"} {
		t.Run(source, func(t *testing.T) {
			entry := domain.Entry{ID: 1, Title: "Article", PublishedAt: date, UpdatedAt: date.Add(-time.Hour), URL: "https://example.test/article",
				HTML: `<p><a href="/link">needle</a></p>` + strings.Repeat("<p>filler</p>", 30)}
			if source == "updated fallback" {
				entry.PublishedAt, entry.UpdatedAt = time.Time{}, date
			}
			m := New(nil, nil, nil)
			m.active, m.readerEntry, m.readerSearch = readerPane, &entry, "just now"
			m.cacheReaderContent(entry, date.Add(10*time.Second))
			m.paintReaderContent()
			if !strings.Contains(ansi.Strip(m.reader.GetContent()), "just now") || len(m.readerMatches) != 1 {
				t.Fatal("initial relative date was not rendered and searchable")
			}
			wrapped, links, matches, pattern := m.readerCache.wrapped, &m.readerLinks[0], &m.readerMatches[0], m.readerCache.pattern
			m.cacheReaderContent(entry, date.Add(59*time.Second))
			if m.readerCache.wrapped != wrapped || &m.readerLinks[0] != links || &m.readerMatches[0] != matches {
				t.Fatal("unchanged relative-date label invalidated cached rendering")
			}
			m.reader.SetYOffset(3)
			m.readerLinkCursor = 0
			m.cacheReaderContent(entry, date.Add(time.Minute))
			m.paintReaderContent()
			content := ansi.Strip(m.reader.GetContent())
			if !strings.Contains(content, "1m ago") || strings.Contains(content, "just now") || len(m.readerMatches) != 0 || m.readerMatchCursor != -1 {
				t.Fatal("relative-date transition retained stale header or search matches")
			}
			if m.reader.YOffset() != 3 || m.readerLinkCursor != 0 || m.readerEntry != &entry || m.readerCache.pattern != pattern {
				t.Fatal("date refresh changed scroll, link selection, snapshot, or query regexp")
			}
			links = &m.readerLinks[0]
			m.selectReaderLink(1)
			if &m.readerLinks[0] != links {
				t.Fatal("Tab reparsed HTML after date refresh")
			}
		})
	}
}

func TestReaderCacheInvalidation(t *testing.T) {
	m := benchmarkReaderModel()
	next, _ := m.enterReader()
	m = next.(Model)
	m.searchReader("needle")
	m.reader.SetYOffset(8)
	wrapped, links, matches, pattern := m.readerCache.wrapped, &m.readerLinks[0], &m.readerMatches[0], m.readerCache.pattern
	m.syncReader()
	if m.readerCache.wrapped != wrapped || &m.readerLinks[0] != links || &m.readerMatches[0] != matches || m.readerCache.pattern != pattern || m.reader.YOffset() != 8 {
		t.Fatal("same article sync discarded cached content, matches, regexp, or scroll")
	}
	m.readerEntry.Read, m.readerEntry.Starred, m.readerEntry.ReadingProgress = true, true, 0.5
	m.syncReader()
	if &m.readerLinks[0] != links || &m.readerMatches[0] != matches {
		t.Fatal("user-state reconciliation invalidated reader presentation")
	}
	m.selectReaderMatch(1)
	m.selectReaderLink(1)
	if &m.readerLinks[0] != links || &m.readerMatches[0] != matches || m.readerCache.pattern != pattern {
		t.Fatal("search/Tab navigation rebuilt links, matches, or regexp")
	}
	m.searchReader("Paragraph")
	if m.readerCache.wrapped != wrapped || &m.readerLinks[0] != links || m.readerCache.pattern == pattern || len(m.readerMatches) != 200 {
		t.Fatal("query change did not reuse rendering and replace search results")
	}
	pattern = m.readerCache.pattern
	m.searchReader("Paragraph")
	if m.readerCache.pattern != pattern {
		t.Fatal("resubmitting the same query recompiled the regexp")
	}
	for _, mutation := range []string{"HTML", "URL", "width", "theme", "title", "text"} {
		t.Run(mutation, func(t *testing.T) {
			before := m.readerCache.wrapped
			m.reader.SetYOffset(5)
			switch mutation {
			case "HTML":
				m.readerEntry.HTML += `<p>Extra <a href="/new">Paragraph link</a></p>`
				m.syncReader()
				if len(m.readerLinks) != 201 || len(m.readerMatches) != 201 {
					t.Fatal("HTML change did not refresh links and matches")
				}
			case "URL":
				m.readerEntry.URL = "https://other.test/article"
				m.syncReader()
				if m.readerLinks[0].URL != "https://other.test/link/0" {
					t.Fatal("base URL change retained old link target")
				}
			case "width":
				m.readerLinkCursor = 3
				m.width = 60
				m.resizeReader()
				if m.readerLinkCursor != 3 {
					t.Fatal("reflow lost selected link identity")
				}
			case "theme":
				styles, err := ui.ResolveScheme("solarized-light")
				if err != nil {
					t.Fatal(err)
				}
				m.applyStyles(styles)
				if m.readerLinkCursor != 3 {
					t.Fatal("theme change lost selected link identity")
				}
			case "title":
				m.readerEntry.Title = "New title"
				m.syncReader()
			case "text":
				m.readerEntry.HTML = ""
				m.readerEntry.Text = strings.Repeat("Paragraph plain text\n", 100)
				m.syncReader()
				if len(m.readerLinks) != 0 || len(m.readerMatches) != 100 {
					t.Fatal("plain-text replacement retained HTML links or matches")
				}
			}
			if m.readerCache.wrapped == before || m.reader.YOffset() != 5 || m.readerCache.pattern != pattern {
				t.Fatal("presentation change failed to invalidate content or preserve scroll/regexp")
			}
		})
	}
	m.searchReader("")
	if len(m.readerMatches) != 0 || m.readerCache.pattern != nil || m.readerMatchCursor != -1 {
		t.Fatal("clearing query retained matches or regexp")
	}
	m.active, m.readerEntry, m.entries = articlesPane, nil, nil
	m.syncReader()
	if m.readerCache.wrapped != "" || m.reader.GetContent() != "No article selected." {
		t.Fatal("empty preview retained cached article")
	}
}

func TestReaderSearchAndTabPreserveOSCIdentity(t *testing.T) {
	m := New(nil, nil, nil)
	m.width, m.height, m.active = 36, 10, articlesPane
	m.entries = []domain.Entry{{ID: 1, URL: "https://example.test/article", HTML: "<p>\u4e00 e\u0301 " + `<a href="/same">needle [a.b] long link words that wrap across several lines</a></p>` + strings.Repeat("<p>filler</p>", 20) + `<p><a href="/same">needle [a.b] again</a></p>`}}
	next, _ := m.enterReader()
	m = next.(Model)
	base := m.reader.GetContent()
	spans := findReaderLinkSpans(base, m.readerLinks)
	if len(spans) != 2 || len(spans[0]) < 2 || spans[0][0].start != 5 {
		t.Fatalf("wrapped Unicode link spans = %#v", spans)
	}
	for _, query := range []string{"[a.b]", "NEEDLE"} {
		m.searchReader(query)
		if len(m.readerMatches) != 2 {
			t.Fatalf("literal/case-insensitive query %q: %#v", query, m.readerMatches)
		}
		m.selectReaderMatch(1)
		match := m.readerMatches[m.readerMatchCursor]
		if match.line < m.reader.YOffset() || match.line >= m.reader.YOffset()+m.reader.Height() {
			t.Fatal("selected match is not visible")
		}
		m.selectReaderLink(-1)
		if m.readerLinkCursor != 1 || m.reader.YOffset() == 0 {
			t.Fatal("reverse Tab did not select and reveal the last link")
		}
		m.selectReaderLink(1)
		if m.readerLinkCursor != 0 {
			t.Fatal("Tab did not wrap to first link")
		}
		got := m.reader.GetContent()
		if ansi.Strip(got) != ansi.Strip(base) || !reflect.DeepEqual(findReaderLinkSpans(got, m.readerLinks), spans) {
			t.Fatalf("search/Tab changed text or OSC link identities: %q", got)
		}
		m, _ = update(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
		if m.readerLinkCursor != -1 || len(m.readerMatches) != 2 {
			t.Fatal("Escape did not clear link selection while preserving search")
		}
	}
	if !m.readerReachedBottom {
		t.Fatal("revealing bottom link did not latch reading completion")
	}
}

func TestReaderSearchAcrossLinkBoundaries(t *testing.T) {
	m := New(nil, nil, nil)
	m.active = articlesPane
	m.entries = []domain.Entry{{ID: 1, URL: "https://example.test/article", HTML: `<p>prefix <a href="/one">one</a> <a href="/two">two</a> suffix</p>`}}
	next, _ := m.enterReader()
	m = next.(Model)
	base := m.reader.GetContent()
	want := findReaderLinkSpans(base, m.readerLinks)
	m.searchReader("prefix one two suffix")
	if len(m.readerMatches) != 1 {
		t.Fatalf("cross-link matches = %#v", m.readerMatches)
	}
	m.selectReaderLink(1)
	if got := m.reader.GetContent(); ansi.Strip(got) != ansi.Strip(base) || !reflect.DeepEqual(findReaderLinkSpans(got, m.readerLinks), want) {
		t.Fatal("cross-link search merged or removed hyperlink identities")
	}
	m.searchReader("missing")
	if m.readerMatchCursor != -1 || len(m.readerMatches) != 0 || !m.errStatus {
		t.Fatal("missing query retained old search results")
	}
}

func TestReaderSearchStartsNearScrollAndNoMatchPreservesScroll(t *testing.T) {
	m := benchmarkReaderModel()
	next, _ := m.enterReader()
	m = next.(Model)
	m.reader.SetYOffset(50)
	m.searchReader("needle")
	if m.readerMatches[m.readerMatchCursor].line < 50 {
		t.Fatal("submission selected a match above the current viewport")
	}
	m.reader.SetYOffset(50)
	m.searchReader("not in this article")
	if m.reader.YOffset() != 50 {
		t.Fatal("unsuccessful search reset scroll")
	}
	m.searchReader("")
	if m.reader.YOffset() != 50 || m.reader.GetContent() != m.readerCache.wrapped {
		t.Fatal("clearing search did not restore unhighlighted content at current scroll")
	}
}

func TestArticleBoundaryIsPresentationNoOp(t *testing.T) {
	m := benchmarkReaderModel()
	opened := m.entries[0]
	m.readerEntry = &opened
	m.reader.SetYOffset(7)
	m.readerLinkCursor = 2
	links := &m.readerLinks[0]
	for _, action := range []func(){func() { m.move(-1) }, func() { m.move(1) }, func() { m.moveToListBoundary(false) }, func() { m.moveToListBoundary(true) }} {
		action()
		if m.readerEntry != &opened || &m.readerLinks[0] != links || m.readerLinkCursor != 2 || m.reader.YOffset() != 7 {
			t.Fatal("clamped movement changed snapshot, links, selection, or scroll")
		}
	}
}

func TestReaderOpeningRestoresProgressAfterGeometry(t *testing.T) {
	m := benchmarkReaderModel()
	m.reader.GotoBottom()
	m.readerReachedBottom = true
	m.readerLinkCursor = 4
	next, _ := m.enterReader()
	m = next.(Model)
	if m.readerCache.width != m.readerTextWidth() || m.readerCache.width != maxReaderTextWidth || !m.reader.AtTop() || m.readerReachedBottom || m.readerLinkCursor != -1 {
		t.Fatal("opening retained preview geometry, scroll, link selection, or completion latch")
	}
	if m.readerEntry == nil || m.readerEntry.ID != m.entries[0].ID {
		t.Fatal("opening did not initialize the reader snapshot")
	}
}
