package app

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/polera/rxs/internal/domain"
)

func TestTruncateTerminalCells(t *testing.T) {
	for _, test := range []struct {
		name, text string
		width      int
		want       string
	}{
		{"ascii", "abcdef", 4, "abc\u2026"},
		{"fits", "abcd", 4, "abcd"},
		{"negative", "abc", -1, ""},
		{"zero", "abc", 0, ""},
		{"one", "abc", 1, "\u2026"},
		{"one fits", "a", 1, "a"},
		{"CJK", "\u4e00\u4e8c\u4e09\u56db", 5, "\u4e00\u4e8c\u2026"},
		{"wide one", "\u4e00", 1, "\u2026"},
		{"combining", "e\u0301e\u0301e\u0301", 2, "e\u0301\u2026"},
		{"joined emoji", "\U0001f469\u200d\U0001f4bbabc", 3, "\U0001f469\u200d\U0001f4bb\u2026"},
		{"emoji too wide", "\U0001f469\u200d\U0001f4bbabc", 2, "\u2026"},
		{"styled", "\x1b[31m\u4e00\u4e8c\u4e09\x1b[0m", 3, "\u4e00\u2026"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := truncate(test.text, test.width)
			if !utf8.ValidString(got) || ansi.Strip(got) != test.want || ansi.StringWidth(got) > max(0, test.width) {
				t.Fatalf("truncate(%q, %d) = %q, want %q within cell budget", test.text, test.width, got, test.want)
			}
			if test.name == "styled" && (!strings.Contains(got, "\x1b[31m") || !strings.HasSuffix(got, "\x1b[0m")) {
				t.Fatalf("lost style or reset: %q", got)
			}
		})
	}
}

func TestReaderFooterUsesCellWidths(t *testing.T) {
	m := New(nil, nil, nil)
	m.active, m.width = readerPane, 25
	got := m.footerText("\u4e00\u4e8c e\u0301 \U0001f469\u200d\U0001f4bb")
	if ansi.StringWidth(got) != m.width-2 || !strings.HasSuffix(got, "% read") {
		t.Fatalf("footer = %q, width = %d", got, ansi.StringWidth(got))
	}
}

func TestVisibleRowsMatchLineWindow(t *testing.T) {
	m := New(nil, nil, nil)
	for i := range 8 {
		m.entries = append(m.entries, domain.Entry{Title: fmt.Sprintf("Article %d", i), FeedTitle: "Feed", Read: i%2 == 0, Starred: i%3 == 0})
		feed := domain.Feed{Title: fmt.Sprintf("Feed %d", i), UnreadCount: i}
		if i%3 != 1 {
			feed.LastError = fmt.Sprintf("error %d", i)
		}
		m.feeds = append(m.feeds, feed)
	}
	m.allFeeds = m.feeds
	// Independent reference: the previous format-all-then-slice policy, including
	// one-line windows and a detail line at either edge of an odd-height window.
	window := func(lines []string, selected, height int) string {
		if len(lines) <= height {
			return strings.Join(lines, "\n")
		}
		last := min(selected+1, len(lines)-1)
		start := min(clamp(last-height+1, 0, len(lines)-height), selected)
		return strings.Join(lines[start:start+height], "\n")
	}
	for _, height := range []int{1, 2, 3, 5, 9, 30} {
		m.height = height + 5
		for cursor := range len(m.feeds) + 2 {
			m.active, m.feedCursor = feedsPane, cursor
			total := 0
			for _, feed := range m.allFeeds {
				total += feed.UnreadCount
			}
			lines := []string{m.menuLine(0, "All", total, 32), m.menuLine(1, "Starred", -1, 32), ""}
			selected := min(cursor, 1)
			for i, feed := range m.feeds {
				if i+2 == cursor {
					selected = len(lines)
				}
				line := m.menuLine(i+2, feed.Title, feed.UnreadCount, 32)
				if feed.LastError != "" {
					line += m.styles.Dim.Render(" !")
				}
				lines = append(lines, line)
				if feed.LastError != "" {
					lines = append(lines, m.styles.Danger.Render("  "+feed.LastError))
				}
			}
			if got, want := m.feedsView(32), window(lines, selected, height); got != want {
				t.Fatalf("feeds height=%d cursor=%d: got %q, want %q", height, cursor, got, want)
			}
		}
		for cursor := range m.entries {
			m.active, m.entryCursor = articlesPane, cursor
			var lines []string
			for i, entry := range m.entries {
				marker := "  "
				if !entry.Read {
					marker = "\u25cf "
				}
				line := marker + entry.Title
				if entry.Starred {
					line += " \u2605"
				}
				if i == cursor {
					line = m.styles.Selected.Width(32).Render(line)
				}
				lines = append(lines, line, m.styles.Dim.Render("  Feed \u00b7 unknown date"))
			}
			if got, want := m.entriesView(32), window(lines, cursor*2, height); got != want {
				t.Fatalf("articles height=%d cursor=%d: got %q, want %q", height, cursor, got, want)
			}
		}
	}
}

func TestEmptyVisibleRows(t *testing.T) {
	m := New(nil, nil, nil)
	for _, filtered := range []bool{false, true} {
		message := "Press a to add a feed."
		if filtered {
			m.allFeeds = []domain.Feed{{UnreadCount: 7}}
			message = "No matching feeds."
		}
		m.height = 9
		if got := ansi.Strip(m.feedsView(32)); !strings.HasSuffix(got, message) {
			t.Fatalf("empty feeds: %q", got)
		}
		m.height = 6
		if got := ansi.Strip(m.feedsView(32)); strings.Contains(got, "\n") || !strings.HasPrefix(got, "All") {
			t.Fatalf("tiny empty feeds: %q", got)
		}
	}
	if got := ansi.Strip(m.entriesView(32)); got != "No matching articles." {
		t.Fatalf("empty articles: %q", got)
	}
}
