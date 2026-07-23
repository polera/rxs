package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/ui"
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

func ctrlKey(value rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: value, Mod: tea.ModCtrl})
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

func TestThemeAppliesBaseColorsSelectionsErrorsAndLinks(t *testing.T) {
	styles, err := ui.ResolveScheme("solarized-light")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		feeds: []domain.Feed{{ID: 1, Title: "Feed", LastError: "refresh failed"}},
		entries: []domain.Entry{{
			ID: 1, Title: "Article", FeedTitle: "Feed", URL: "https://example.test/article",
			HTML: `<p>Visit <a href="/reference">the reference</a>.</p>`, Text: "Visit the reference.",
		}},
	}
	model := New(store, fakeRefresher{}, func(string) error { return nil }, styles)
	model, _ = update(t, model, loadedMsg{feeds: store.feeds, entries: store.entries})

	view := model.View()
	if !reflect.DeepEqual(view.ForegroundColor, styles.Scheme.Foreground) ||
		!reflect.DeepEqual(view.BackgroundColor, styles.Scheme.Background) {
		t.Fatalf("view base colors = %#v on %#v, want %#v on %#v", view.ForegroundColor, view.BackgroundColor, styles.Scheme.Foreground, styles.Scheme.Background)
	}
	if selected := model.menuLine(0, "All", 0, 20); selected != styles.Selected.Width(20).Render("All 0") {
		t.Fatalf("selected row = %q", selected)
	}
	if feedView := model.feedsView(30); !strings.Contains(feedView, styles.Danger.Render("  refresh failed")) {
		t.Fatalf("feed error does not use danger style: %q", feedView)
	}

	model.setReaderContent(store.entries[0])
	wantLink := styles.Link.Hyperlink("https://example.test/reference", "id=rxs-link-0").Render("the reference")
	if content := model.reader.GetContent(); !strings.Contains(content, wantLink) {
		t.Fatalf("reader link does not use link style: %q", content)
	}

	model.overlay = addOverlay
	overlay := model.View()
	if !reflect.DeepEqual(overlay.ForegroundColor, styles.Scheme.Foreground) ||
		!reflect.DeepEqual(overlay.BackgroundColor, styles.Scheme.Background) {
		t.Fatal("overlay did not retain the theme base colors")
	}
	inputStyles := model.input.Styles()
	if !reflect.DeepEqual(inputStyles.Cursor.Color, styles.Scheme.Accent) ||
		inputStyles.Focused.Placeholder.Render("hint") != styles.Dim.Render("hint") {
		t.Fatal("text input did not use the theme styles")
	}
}

func TestWarningStatusUsesWarningStyleAndDoesNotBlockLoading(t *testing.T) {
	styles, err := ui.ResolveScheme("solarized-light")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		feeds:   []domain.Feed{{ID: 1, Title: "Feed"}},
		entries: []domain.Entry{{ID: 1, Title: "Article"}},
	}
	model := New(store, fakeRefresher{}, func(string) error { return nil }, styles)
	warning := `browser command "missing-browser" was not found`
	model.SetWarningStatus(warning)

	model, cmd := update(t, model, loadedMsg{feeds: store.feeds, entries: store.entries})
	if cmd != nil || model.status != warning || model.errStatus {
		t.Fatalf("status = %q, error = %t, cmd = %v", model.status, model.errStatus, cmd)
	}
	if view := model.View().Content; !strings.Contains(view, styles.Warning.Render(warning)) {
		t.Fatalf("warning status does not use warning style: %q", view)
	}

	model, _ = update(t, model, browserMsg{target: "link"})
	if model.status != "Opened link" || model.status == model.warningStatus {
		t.Fatalf("replacement status = %q, warning = %q", model.status, model.warningStatus)
	}
}

func TestColorSchemeChooserPreviewsAndPersistsSelection(t *testing.T) {
	model, _ := loadedModel(t)
	var saved string
	model.SetColorSchemeSaver(func(name string) error {
		saved = name
		return nil
	})

	model, _ = update(t, model, key('c'))
	if model.overlay != colorSchemeOverlay {
		t.Fatalf("c opened overlay %v", model.overlay)
	}
	model, _ = update(t, model, key('j'))
	if model.styles.Name != "dracula" {
		t.Fatalf("previewed scheme = %q, want dracula", model.styles.Name)
	}
	dracula, err := ui.ResolveScheme("dracula")
	if err != nil {
		t.Fatal(err)
	}
	view := model.View()
	if !reflect.DeepEqual(view.ForegroundColor, dracula.Scheme.Foreground) ||
		!reflect.DeepEqual(view.BackgroundColor, dracula.Scheme.Background) {
		t.Fatal("preview did not update the terminal colors")
	}

	model, cmd := update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if model.overlay != noOverlay || cmd == nil {
		t.Fatalf("selection did not close and schedule persistence: overlay=%v cmd=%v", model.overlay, cmd)
	}
	model, _ = update(t, model, cmd())
	if saved != "dracula" {
		t.Fatalf("saved scheme = %q, want dracula", saved)
	}
	if model.styles.Name != "dracula" || model.errStatus {
		t.Fatalf("selected scheme was not retained: styles=%q status=%q", model.styles.Name, model.status)
	}
}

func TestColorSchemeChooserCancelRestoresOriginal(t *testing.T) {
	styles, err := ui.ResolveScheme("nord")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{}
	model := New(store, fakeRefresher{}, func(string) error { return nil }, styles)
	saves := 0
	model.SetColorSchemeSaver(func(string) error {
		saves++
		return nil
	})

	model, _ = update(t, model, key('c'))
	model, _ = update(t, model, key('j'))
	if model.styles.Name == "nord" {
		t.Fatal("navigation did not preview another scheme")
	}
	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if model.overlay != noOverlay || model.styles.Name != "nord" {
		t.Fatalf("cancel left overlay=%v scheme=%q", model.overlay, model.styles.Name)
	}
	if saves != 0 {
		t.Fatalf("cancel saved %d times", saves)
	}
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

func TestFeedListScrollsToKeepSelectionVisible(t *testing.T) {
	store := &fakeStore{}
	for i := 0; i < 12; i++ {
		store.feeds = append(store.feeds, domain.Feed{ID: int64(i + 1), Title: fmt.Sprintf("Feed %02d", i)})
	}
	model := New(store, fakeRefresher{}, func(string) error { return nil })
	model, _ = update(t, model, loadedMsg{feeds: store.feeds})
	model, _ = update(t, model, tea.WindowSizeMsg{Width: 50, Height: 10})

	for range len(store.feeds) + 1 {
		model, _ = update(t, model, key('j'))
	}
	view := ansi.Strip(model.View().Content)
	if !strings.Contains(view, "Feed 11") {
		t.Fatalf("selected feed was clipped after navigating down: %q", view)
	}
	if strings.Contains(view, "All 0") {
		t.Fatalf("feed list did not scroll away from its first row: %q", view)
	}
}

func TestArticleListScrollsToKeepSelectionVisible(t *testing.T) {
	model, _ := loadedModel(t)
	model.entries = nil
	for i := 0; i < 12; i++ {
		model.entries = append(model.entries, domain.Entry{Title: fmt.Sprintf("Article %02d", i), FeedTitle: "Feed"})
	}
	model.active = articlesPane
	model.entryCursor = len(model.entries) - 1
	model, _ = update(t, model, tea.WindowSizeMsg{Width: 80, Height: 10})

	view := ansi.Strip(model.View().Content)
	if !strings.Contains(view, "Article 11") {
		t.Fatalf("selected article was clipped after navigating down: %q", view)
	}
	if strings.Contains(view, "Article 00") {
		t.Fatalf("article list did not scroll away from its first row: %q", view)
	}
}

func TestListVimNavigation(t *testing.T) {
	store := &fakeStore{
		feeds: []domain.Feed{
			{ID: 1, Title: "First"},
			{ID: 2, Title: "Last"},
		},
		entries: []domain.Entry{
			{ID: 10, Title: "First article"},
			{ID: 11, Title: "Last article"},
		},
	}
	model := New(store, fakeRefresher{}, func(string) error { return nil })
	model, _ = update(t, model, loadedMsg{feeds: store.feeds, entries: store.entries})

	var cmd tea.Cmd
	model, cmd = update(t, model, key('G'))
	if model.feedCursor != len(store.feeds)+1 || model.filter.FeedID != store.feeds[1].ID || cmd == nil {
		t.Fatalf("feed G: cursor=%d filter=%+v cmd=%v", model.feedCursor, model.filter, cmd)
	}
	model, _ = update(t, model, key('g'))
	model, cmd = update(t, model, key('g'))
	if model.feedCursor != 0 || model.filter.FeedID != 0 || cmd == nil {
		t.Fatalf("feed gg: cursor=%d filter=%+v cmd=%v", model.feedCursor, model.filter, cmd)
	}

	model.active = articlesPane
	model, _ = update(t, model, key('G'))
	if model.entryCursor != len(store.entries)-1 {
		t.Fatalf("article G cursor=%d, want %d", model.entryCursor, len(store.entries)-1)
	}
	model, _ = update(t, model, key('g'))
	model, _ = update(t, model, key('g'))
	if model.entryCursor != 0 {
		t.Fatalf("article gg cursor=%d, want 0", model.entryCursor)
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

func TestReaderPagesWithVimControlKeys(t *testing.T) {
	model, _ := loadedModel(t)
	model.entries[0].Text = strings.Repeat("readable prose ", 400)
	model.active = readerPane
	model, _ = update(t, model, tea.WindowSizeMsg{Width: 80, Height: 24})
	model.setReaderContent(model.entries[0])
	model.reader.GotoTop()

	page := model.reader.Height()
	model, _ = update(t, model, ctrlKey('f'))
	if got := model.reader.YOffset(); got != page {
		t.Fatalf("ctrl+f offset = %d, want %d", got, page)
	}
	model, _ = update(t, model, ctrlKey('d'))
	if got := model.reader.YOffset(); got != page+page/2 {
		t.Fatalf("ctrl+d offset = %d, want %d", got, page+page/2)
	}
	model, _ = update(t, model, ctrlKey('u'))
	if got := model.reader.YOffset(); got != page {
		t.Fatalf("ctrl+u offset = %d, want %d", got, page)
	}
	model, _ = update(t, model, ctrlKey('b'))
	if got := model.reader.YOffset(); got != 0 {
		t.Fatalf("ctrl+b offset = %d, want 0", got)
	}
}

func TestFeedListPagesWithVimControlKeys(t *testing.T) {
	store := &fakeStore{}
	for i := 0; i < 12; i++ {
		store.feeds = append(store.feeds, domain.Feed{ID: int64(i + 1), Title: fmt.Sprintf("Feed %02d", i)})
	}
	model := New(store, fakeRefresher{}, func(string) error { return nil })
	model, _ = update(t, model, loadedMsg{feeds: store.feeds})
	model, _ = update(t, model, tea.WindowSizeMsg{Width: 50, Height: 10})

	page := model.listViewHeight()
	model, cmd := update(t, model, ctrlKey('f'))
	if model.feedCursor != page || model.filter.FeedID != store.feeds[page-2].ID || cmd == nil {
		t.Fatalf("ctrl+f: cursor=%d filter=%+v cmd=%v, want cursor %d and a reload", model.feedCursor, model.filter, cmd, page)
	}
	model, cmd = update(t, model, ctrlKey('b'))
	if model.feedCursor != 0 || model.filter.FeedID != 0 || cmd == nil {
		t.Fatalf("ctrl+b: cursor=%d filter=%+v cmd=%v, want first row and a reload", model.feedCursor, model.filter, cmd)
	}
}

func TestFeedPagingKeysDoNothingInArticleList(t *testing.T) {
	model, _ := loadedModel(t)
	model.active = articlesPane
	model, cmd := update(t, model, ctrlKey('f'))
	if model.active != articlesPane || model.entryCursor != 0 || cmd != nil {
		t.Fatalf("ctrl+f in articles moved to pane %d, cursor %d with cmd %v", model.active, model.entryCursor, cmd)
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

func TestQuitRequiresConfirmation(t *testing.T) {
	model, _ := loadedModel(t)

	model, cmd := update(t, model, key('q'))
	if model.overlay != quitOverlay || cmd != nil {
		t.Fatalf("q returned overlay=%v cmd=%v, want quit confirmation", model.overlay, cmd)
	}
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, "Are you sure you want to quit?") {
		t.Fatalf("quit confirmation is missing from view: %q", view)
	}

	model, cmd = update(t, model, key('n'))
	if model.overlay != noOverlay || cmd != nil {
		t.Fatalf("n did not cancel quit: overlay=%v cmd=%v", model.overlay, cmd)
	}

	model, cmd = update(t, model, ctrlKey('c'))
	if model.overlay != quitOverlay || cmd != nil {
		t.Fatalf("ctrl+c returned overlay=%v cmd=%v, want quit confirmation", model.overlay, cmd)
	}
	model, cmd = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("confirming quit returned no command")
	}
	message := cmd()
	if _, ok := message.(tea.QuitMsg); !ok {
		t.Fatalf("confirming quit returned %T, want tea.QuitMsg", message)
	}
}

func TestFeedFilterMatchesTitleAndURLAndCanBeCleared(t *testing.T) {
	store := &fakeStore{
		feeds: []domain.Feed{
			{ID: 1, Title: "Go Blog", URL: "https://go.dev/blog/feed.atom"},
			{ID: 2, Title: "Project News", URL: "https://example.test/releases.xml"},
			{ID: 3, Title: "Daily Notes", URL: "https://notes.test/feed"},
		},
	}
	model := New(store, fakeRefresher{}, func(string) error { return nil })
	model, _ = update(t, model, loadedMsg{feeds: store.feeds})

	model, _ = update(t, model, key('/'))
	if model.overlay != feedFilterOverlay {
		t.Fatalf("/ in feeds opened overlay %v, want feed filter", model.overlay)
	}
	for _, character := range "RELEASES" {
		model, _ = update(t, model, key(character))
	}
	model, cmd := update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil || model.feedFilter != "RELEASES" || len(model.feeds) != 1 || model.feeds[0].ID != 2 {
		t.Fatalf("filtered model: term=%q feeds=%#v cmd=%v", model.feedFilter, model.feeds, cmd)
	}
	if len(model.allFeeds) != 3 || model.feedCursor != 0 || model.filter.FeedID != 0 {
		t.Fatalf("filter changed backing feeds or selection: all=%d cursor=%d filter=%+v", len(model.allFeeds), model.feedCursor, model.filter)
	}

	model, _ = update(t, model, cmd())
	model, _ = update(t, model, key('/'))
	model.input.SetValue("")
	model, cmd = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil || model.feedFilter != "" || len(model.feeds) != 3 {
		t.Fatalf("cleared filter: term=%q feeds=%d cmd=%v", model.feedFilter, len(model.feeds), cmd)
	}
}

func TestSearchInputPreservesCurrentTerms(t *testing.T) {
	model, _ := loadedModel(t)
	model.filter.Search = "current terms"
	model.active = articlesPane

	model, _ = update(t, model, key('/'))
	if model.overlay != searchOverlay || model.input.Value() != "current terms" {
		t.Fatalf("search opened with overlay=%v value=%q", model.overlay, model.input.Value())
	}
	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	model, _ = update(t, model, key('/'))
	if model.input.Value() != "current terms" {
		t.Fatalf("reopened search value=%q, want current terms", model.input.Value())
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

func TestArticleLoadingStatusClearsWhenLoadCompletes(t *testing.T) {
	model, _ := loadedModel(t)
	model.errStatus = true

	model, cmd := update(t, model, key('j'))
	if model.status != "Loading articles…" || model.errStatus || cmd == nil {
		t.Fatalf("loading status = %q, error = %t, cmd = %v", model.status, model.errStatus, cmd)
	}

	model, _ = update(t, model, cmd())
	if model.status != "Ready" || model.errStatus {
		t.Fatalf("completed status = %q, error = %t", model.status, model.errStatus)
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
	content := model.reader.GetContent()
	plain := ansi.Strip(content)
	if !strings.Contains(plain, "Read the related article next.") || strings.Contains(plain, "[1]") ||
		strings.Contains(plain, "\nLinks\n") || strings.Contains(plain, "https://example.test/related") {
		t.Fatalf("reader content did not keep the link inline: %q", plain)
	}
	if !strings.Contains(content, ansi.SetHyperlink("https://example.test/related", "id=rxs-link-0")) {
		t.Fatalf("reader content did not hyperlink the anchor text: %q", content)
	}
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
