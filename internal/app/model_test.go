package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/polera/rxs/internal/domain"
)

type fakeStore struct {
	feeds      []domain.Feed
	entries    []domain.Entry
	readCalls  []bool
	starCalls  []bool
	lastFilter domain.EntryFilter
}

func (s *fakeStore) AddFeed(context.Context, string) (domain.Feed, error) {
	return domain.Feed{}, errors.New("not implemented")
}
func (s *fakeStore) DeleteFeed(context.Context, int64) error { return nil }
func (s *fakeStore) Feeds(context.Context) ([]domain.Feed, error) {
	return append([]domain.Feed(nil), s.feeds...), nil
}
func (s *fakeStore) Entries(_ context.Context, filter domain.EntryFilter) ([]domain.Entry, error) {
	s.lastFilter = filter
	return append([]domain.Entry(nil), s.entries...), nil
}
func (s *fakeStore) SetRead(_ context.Context, _ int64, value bool) error {
	s.readCalls = append(s.readCalls, value)
	return nil
}
func (s *fakeStore) SetStarred(_ context.Context, _ int64, value bool) error {
	s.starCalls = append(s.starCalls, value)
	return nil
}

type fakeRefresher struct{}

func (fakeRefresher) Refresh(context.Context, int64) domain.RefreshResult {
	return domain.RefreshResult{}
}
func (fakeRefresher) RefreshAll(context.Context, []domain.Feed, int) []domain.RefreshResult {
	return nil
}

func key(value rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: value, Text: string(value)})
}

func update(t *testing.T, model Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := model.Update(msg)
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return got, cmd
}

func loadedModel(t *testing.T) (Model, *fakeStore) {
	t.Helper()
	store := &fakeStore{
		feeds: []domain.Feed{{ID: 1, Title: "Feed", URL: "https://example.test/feed"}},
		entries: []domain.Entry{
			{ID: 10, FeedID: 1, FeedTitle: "Feed", Title: "First", Text: "one"},
			{ID: 11, FeedID: 1, FeedTitle: "Feed", Title: "Second", Text: "two"},
		},
	}
	model := New(store, fakeRefresher{}, func(string) error { return nil })
	var cmd tea.Cmd
	model, cmd = update(t, model, loadedMsg{feeds: store.feeds, entries: store.entries})
	if cmd != nil {
		t.Fatal("loading unexpectedly returned a command")
	}
	return model, store
}

func TestHighlightDoesNotMarkReadButOpeningDoes(t *testing.T) {
	model, store := loadedModel(t)
	model, _ = update(t, model, key('l'))
	model, _ = update(t, model, key('j'))
	if len(store.readCalls) != 0 {
		t.Fatal("highlighting an article marked it read")
	}
	model, cmd := update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if model.active != readerPane || cmd == nil {
		t.Fatal("enter did not open the reader and schedule state persistence")
	}
	_ = cmd()
	if len(store.readCalls) != 1 || !store.readCalls[0] {
		t.Fatalf("read calls = %v", store.readCalls)
	}
	if model.readerEntry == nil || model.readerEntry.ID != 11 {
		t.Fatalf("opened entry = %#v", model.readerEntry)
	}
}

func TestResponsivePaneVisibility(t *testing.T) {
	model, _ := loadedModel(t)
	model, _ = update(t, model, tea.WindowSizeMsg{Width: 60, Height: 20})
	view := model.View().Content
	if !strings.Contains(view, "Feeds") || strings.Contains(view, "Articles") {
		t.Fatalf("narrow feed view has unexpected panes: %q", view)
	}
	model.active = readerPane
	model.resizeReader()
	model.syncReader()
	view = model.View().Content
	if !strings.Contains(view, "Reader") || strings.Contains(view, "Feeds") {
		t.Fatalf("narrow reader view has unexpected panes: %q", view)
	}
	model.width = 120
	view = model.View().Content
	if !strings.Contains(view, "Reader") || strings.Contains(view, "Feeds") || strings.Contains(view, "Articles") {
		t.Fatalf("wide reader view has unexpected panes: %q", view)
	}
}

func TestReaderFocusExpandsAndBrowsingRestoresWideLayout(t *testing.T) {
	model, _ := loadedModel(t)
	model, _ = update(t, model, tea.WindowSizeMsg{Width: 120, Height: 30})
	browsingWidth := model.reader.Width()
	model, _ = update(t, model, key('l'))
	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	view := model.View().Content
	if !strings.Contains(view, "Reader") || strings.Contains(view, "Feeds") || strings.Contains(view, "Articles") {
		t.Fatalf("focused reader has unexpected panes: %q", view)
	}
	if model.reader.Width() <= browsingWidth {
		t.Fatalf("focused reader width = %d, browsing width = %d", model.reader.Width(), browsingWidth)
	}

	model, _ = update(t, model, key('h'))
	view = model.View().Content
	for _, title := range []string{"Feeds", "Articles", "Reader"} {
		if !strings.Contains(view, title) {
			t.Fatalf("restored wide view missing %s: %q", title, view)
		}
	}
	if model.reader.Width() != browsingWidth {
		t.Fatalf("restored reader width = %d, want %d", model.reader.Width(), browsingWidth)
	}
}

func TestReaderWrapsAtWordBoundaries(t *testing.T) {
	model, _ := loadedModel(t)
	model.entries[0].Text = "alpha bravo charlie delta echo"
	model, _ = update(t, model, tea.WindowSizeMsg{Width: 30, Height: 20})
	model.active = readerPane
	model.resizeReader()
	model.setReaderContent(model.entries[0])

	content := model.reader.GetContent()
	if !strings.Contains(content, "alpha bravo charlie\ndelta echo") {
		t.Fatalf("reader did not wrap at a word boundary: %q", content)
	}
	if strings.Contains(content, "charlie de\nlta") {
		t.Fatalf("reader split a word: %q", content)
	}
}

func TestReaderReflowsAndLimitsWideLines(t *testing.T) {
	model, _ := loadedModel(t)
	model.entries[0].Text = strings.Repeat("readable prose ", 20)
	model.active = readerPane
	model, _ = update(t, model, tea.WindowSizeMsg{Width: 140, Height: 30})
	model.setReaderContent(model.entries[0])

	if got := model.readerTextWidth(); got != maxReaderTextWidth {
		t.Fatalf("wide reader text width = %d, want %d", got, maxReaderTextWidth)
	}
	for _, line := range strings.Split(model.reader.GetContent(), "\n") {
		if width := lipgloss.Width(line); width > maxReaderTextWidth {
			t.Fatalf("reader line width = %d, want <= %d: %q", width, maxReaderTextWidth, line)
		}
	}

	model, _ = update(t, model, tea.WindowSizeMsg{Width: 50, Height: 30})
	if got := model.readerTextWidth(); got != 42 {
		t.Fatalf("resized reader text width = %d, want 42", got)
	}
	for _, line := range strings.Split(model.reader.GetContent(), "\n") {
		if width := lipgloss.Width(line); width > 42 {
			t.Fatalf("resized reader line width = %d, want <= 42: %q", width, line)
		}
	}
}

func TestQCanBeTypedInInput(t *testing.T) {
	model, _ := loadedModel(t)
	model, _ = update(t, model, key('a'))
	model, _ = update(t, model, key('q'))
	if model.overlay != addOverlay || model.input.Value() != "q" {
		t.Fatalf("q should remain in input: overlay=%v value=%q", model.overlay, model.input.Value())
	}
}

func TestStaleLoadIsIgnored(t *testing.T) {
	model, _ := loadedModel(t)
	model.filter.Search = "current"
	model, _ = update(t, model, loadedMsg{filter: domain.EntryFilter{Search: "old"}, entries: []domain.Entry{{Title: "stale"}}})
	if len(model.entries) != 2 {
		t.Fatalf("stale load replaced entries: %#v", model.entries)
	}
}

func TestReaderLinksCanBeSelectedAndOpened(t *testing.T) {
	store := &fakeStore{
		feeds: []domain.Feed{{ID: 1, Title: "Feed", URL: "https://example.test/feed"}},
		entries: []domain.Entry{{
			ID: 10, FeedID: 1, FeedTitle: "Feed", Title: "First",
			URL:  "https://example.test/articles/first",
			HTML: `<p>Read <a href="/related">the related article</a> next.</p>`,
			Text: "Read the related article next.",
		}},
	}
	var openedURL string
	model := New(store, fakeRefresher{}, func(rawURL string) error {
		openedURL = rawURL
		return nil
	})
	model, _ = update(t, model, loadedMsg{feeds: store.feeds, entries: store.entries})
	model, _ = update(t, model, key('l'))
	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if model.readerLinkCursor != 0 || !strings.Contains(model.status, "the related article") {
		t.Fatalf("selected link = %d, status = %q", model.readerLinkCursor, model.status)
	}
	var cmd tea.Cmd
	model, cmd = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("opening a selected link returned no command")
	}
	message := cmd()
	if openedURL != "https://example.test/related" {
		t.Fatalf("opened URL = %q", openedURL)
	}
	model, _ = update(t, model, message)
	if model.status != "Opened link" {
		t.Fatalf("status = %q", model.status)
	}
}

func TestReaderLinkSelectionWraps(t *testing.T) {
	model, _ := loadedModel(t)
	model.entries[0].URL = "https://example.test/article"
	model.entries[0].HTML = `<a href="/one">One</a><a href="/two">Two</a>`
	model.active = readerPane
	model.setReaderContent(model.entries[0])
	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if model.readerLinkCursor != 1 {
		t.Fatalf("shift-tab selected link %d, want 1", model.readerLinkCursor)
	}
	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if model.readerLinkCursor != 0 {
		t.Fatalf("tab selected link %d, want 0", model.readerLinkCursor)
	}
}

func TestReaderVimNavigation(t *testing.T) {
	model, _ := loadedModel(t)
	model.entries[0].Text = strings.Repeat("line\n", 40)
	model.active = readerPane
	model.height = 12
	model.resizeReader()
	model.setReaderContent(model.entries[0])

	model, _ = update(t, model, key('j'))
	if got := model.reader.YOffset(); got != 1 {
		t.Fatalf("j offset = %d, want 1", got)
	}
	model, _ = update(t, model, key('k'))
	if got := model.reader.YOffset(); got != 0 {
		t.Fatalf("k offset = %d, want 0", got)
	}

	model, _ = update(t, model, key('G'))
	if !model.reader.AtBottom() {
		t.Fatalf("G offset = %d, want bottom", model.reader.YOffset())
	}
	model, _ = update(t, model, key('g'))
	model, _ = update(t, model, key('g'))
	if !model.reader.AtTop() {
		t.Fatalf("gg offset = %d, want top", model.reader.YOffset())
	}
}

func TestReaderSearchFindsAndNavigatesMatches(t *testing.T) {
	model, _ := loadedModel(t)
	model.entries[0].Text = "start\nneedle one\n" + strings.Repeat("filler\n", 20) + "needle two"
	model.active = readerPane
	model.height = 10
	model.resizeReader()
	model.setReaderContent(model.entries[0])

	model, _ = update(t, model, key('/'))
	if model.overlay != readerSearchOverlay {
		t.Fatalf("reader / opened overlay %v", model.overlay)
	}
	for _, character := range "needle" {
		model, _ = update(t, model, key(character))
	}
	var cmd tea.Cmd
	model, cmd = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd != nil {
		t.Fatal("reader search unexpectedly loaded articles")
	}
	if model.filter.Search != "" {
		t.Fatalf("reader search changed article filter to %q", model.filter.Search)
	}
	if len(model.readerMatches) != 2 || model.readerMatchCursor != 0 {
		t.Fatalf("matches = %#v, cursor = %d", model.readerMatches, model.readerMatchCursor)
	}
	firstOffset := model.reader.YOffset()

	model, _ = update(t, model, key('n'))
	if model.readerMatchCursor != 1 || model.reader.YOffset() <= firstOffset {
		t.Fatalf("next match cursor = %d, offset = %d; first offset = %d", model.readerMatchCursor, model.reader.YOffset(), firstOffset)
	}
	model, _ = update(t, model, key('N'))
	if model.readerMatchCursor != 0 {
		t.Fatalf("previous match cursor = %d, want 0", model.readerMatchCursor)
	}
}
