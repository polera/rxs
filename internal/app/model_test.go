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
	feeds         []domain.Feed
	entries       []domain.Entry
	addURLs       []string
	addFeed       domain.Feed
	addErr        error
	readCalls     []bool
	readIDs       []int64
	starCalls     []bool
	lastFilter    domain.EntryFilter
	readErr       error
	persistRead   bool
	progressCalls []float64
	progressIDs   []int64
}

func (s *fakeStore) AddFeed(_ context.Context, rawURL string) (domain.Feed, error) {
	s.addURLs = append(s.addURLs, rawURL)
	return s.addFeed, s.addErr
}
func (s *fakeStore) DeleteFeed(context.Context, int64) error { return nil }
func (s *fakeStore) Feeds(context.Context) ([]domain.Feed, error) {
	return append([]domain.Feed(nil), s.feeds...), nil
}
func (s *fakeStore) Entries(_ context.Context, filter domain.EntryFilter) ([]domain.Entry, error) {
	s.lastFilter = filter
	entries := make([]domain.Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		if filter.UnreadOnly && entry.Read {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
func (s *fakeStore) SetRead(_ context.Context, id int64, value bool) error {
	s.readCalls = append(s.readCalls, value)
	s.readIDs = append(s.readIDs, id)
	if s.readErr != nil {
		return s.readErr
	}
	if s.persistRead {
		for index := range s.entries {
			if s.entries[index].ID == id {
				s.entries[index].Read = value
			}
		}
	}
	return nil
}
func (s *fakeStore) SetStarred(_ context.Context, _ int64, value bool) error {
	s.starCalls = append(s.starCalls, value)
	return nil
}
func (s *fakeStore) SetReadingProgress(_ context.Context, id int64, progress float64) error {
	s.progressIDs = append(s.progressIDs, id)
	s.progressCalls = append(s.progressCalls, progress)
	for index := range s.entries {
		if s.entries[index].ID == id {
			s.entries[index].ReadingProgress = progress
		}
	}
	return nil
}

type fakeRefresher struct{}

func (fakeRefresher) Refresh(context.Context, int64) domain.RefreshResult {
	return domain.RefreshResult{}
}
func (fakeRefresher) RefreshAll(context.Context, []domain.Feed, int) []domain.RefreshResult {
	return nil
}

type recordingRefresher struct {
	feeds   []domain.Feed
	workers int
	calls   int
}

func (r *recordingRefresher) Refresh(context.Context, int64) domain.RefreshResult {
	return domain.RefreshResult{}
}

func (r *recordingRefresher) RefreshAll(_ context.Context, feeds []domain.Feed, workers int) []domain.RefreshResult {
	r.calls++
	r.feeds = append([]domain.Feed(nil), feeds...)
	r.workers = workers
	return []domain.RefreshResult{{FeedID: feeds[0].ID, Added: 2}}
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
	model, cmd = update(t, model, loadedMsg{
		feeds:   append([]domain.Feed(nil), store.feeds...),
		entries: append([]domain.Entry(nil), store.entries...),
	})
	if cmd != nil {
		t.Fatal("loading unexpectedly returned a command")
	}
	return model, store
}

func TestInitRefreshesFeedsAfterInitialLoad(t *testing.T) {
	store := &fakeStore{
		feeds: []domain.Feed{
			{ID: 1, Title: "First"},
			{ID: 2, Title: "Second"},
		},
	}
	refresher := &recordingRefresher{}
	model := New(store, refresher, func(string) error { return nil })

	model, refreshCmd := update(t, model, model.Init()())
	if refreshCmd == nil || !model.busy || model.status != "Refreshing 2 feed(s)…" {
		t.Fatalf("initial load: status=%q busy=%t cmd=%v", model.status, model.busy, refreshCmd)
	}

	model, reloadCmd := update(t, model, refreshCmd())
	if refresher.calls != 1 || refresher.workers != 4 || !reflect.DeepEqual(refresher.feeds, store.feeds) {
		t.Fatalf("refresh: calls=%d workers=%d feeds=%#v", refresher.calls, refresher.workers, refresher.feeds)
	}
	if reloadCmd == nil || model.busy || model.status != "Refresh finished: 2 new article(s)" {
		t.Fatalf("refresh result: status=%q busy=%t cmd=%v", model.status, model.busy, reloadCmd)
	}

	model, nextCmd := update(t, model, reloadCmd())
	if nextCmd != nil || refresher.calls != 1 {
		t.Fatalf("reload scheduled another refresh: calls=%d cmd=%v", refresher.calls, nextCmd)
	}
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

func TestOpeningDoesNotImmediatelyMarkRead(t *testing.T) {
	model, store := loadedModel(t)
	model, _ = update(t, model, key('l'))
	model, _ = update(t, model, key('j'))
	if len(store.readCalls) != 0 {
		t.Fatal("highlighting an article marked it read")
	}
	model, cmd := update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if model.active != readerPane || cmd != nil {
		t.Fatal("enter did not open the reader without scheduling state persistence")
	}
	if len(store.readCalls) != 0 {
		t.Fatalf("read calls = %v", store.readCalls)
	}
	if model.readerEntry == nil || model.readerEntry.ID != 11 {
		t.Fatalf("opened entry = %#v", model.readerEntry)
	}
}

func TestPreviewMarkReadOnScroll(t *testing.T) {
	model, store := loadedModel(t)
	model.SetMarkReadOnScroll(true)
	model.active = articlesPane

	model, cmd := update(t, model, key('j'))
	if cmd == nil || model.entryCursor != 1 || !model.entries[0].Read || model.entries[1].Read {
		t.Fatalf("scroll result: cursor=%d entries=%#v cmd=%v", model.entryCursor, model.entries, cmd)
	}
	_ = cmd()
	if !reflect.DeepEqual(store.readIDs, []int64{10}) || !reflect.DeepEqual(store.readCalls, []bool{true}) {
		t.Fatalf("read writes: ids=%v values=%v", store.readIDs, store.readCalls)
	}
}

func TestPreviewMarkReadOnScrollDisabledBoundariesAndAlreadyRead(t *testing.T) {
	model, store := loadedModel(t)
	model.active = articlesPane

	model, cmd := update(t, model, key('j'))
	if cmd != nil || len(store.readCalls) != 0 {
		t.Fatal("disabled preview marking scheduled a write")
	}

	model.SetMarkReadOnScroll(true)
	model, cmd = update(t, model, key('j'))
	if cmd != nil {
		t.Fatal("clamped movement scheduled a write")
	}
	model.entries[1].Read = true
	model, cmd = update(t, model, key('k'))
	if cmd != nil {
		t.Fatal("leaving an already-read entry scheduled a write")
	}
}

func TestEnteringReaderThroughPaneNavigationTracksArticleWithoutMarkingItImmediately(t *testing.T) {
	model, store := loadedModel(t)
	model.SetMarkReadOnScroll(true)
	model.entries[0].Text = strings.Repeat("long article line\n", 100)
	model.active = articlesPane
	model.height = 12

	model, cmd := update(t, model, key('l'))
	if cmd != nil || model.active != readerPane || model.readerEntry == nil ||
		model.readerEntry.ID != 10 || model.readerReachedBottom {
		t.Fatalf("changing to reader pane: active=%d entry=%#v bottom=%t cmd=%v",
			model.active, model.readerEntry, model.readerReachedBottom, cmd)
	}
	model, cmd = update(t, model, key('h'))
	if cmd == nil || model.active != articlesPane || len(store.readCalls) != 0 {
		t.Fatalf("changing back: active=%d reads=%v cmd=%v", model.active, store.readCalls, cmd)
	}
	_ = cmd()
	if !reflect.DeepEqual(store.progressIDs, []int64{10}) {
		t.Fatalf("progress writes: ids=%v", store.progressIDs)
	}
}

func TestPaneNavigationReaderMarksReadAfterViewingFullArticle(t *testing.T) {
	for _, entryKey := range []tea.KeyPressMsg{
		key('l'),
		tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}),
		tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}),
	} {
		t.Run(entryKey.String(), func(t *testing.T) {
			model, store := loadedModel(t)
			model.entries[0].Text = strings.Repeat("long article line\n", 100)
			model.active = articlesPane
			model.height = 12

			model, _ = update(t, model, entryKey)
			model, _ = update(t, model, key('G'))
			model, cmd := update(t, model, key('h'))
			if cmd == nil || !model.entries[0].Read {
				t.Fatalf("reader exit did not schedule read: entry=%#v cmd=%v", model.entries[0], cmd)
			}
			_ = cmd()
			if !reflect.DeepEqual(store.readIDs, []int64{10}) ||
				!reflect.DeepEqual(store.readCalls, []bool{true}) {
				t.Fatalf("read writes: ids=%v values=%v", store.readIDs, store.readCalls)
			}
		})
	}
}

func TestPreviewJumpMarksOnlyArticleBeingLeft(t *testing.T) {
	model, store := loadedModel(t)
	model.entries = append(model.entries, domain.Entry{ID: 12, Title: "Third"})
	model.SetMarkReadOnScroll(true)
	model.active = articlesPane

	model, cmd := update(t, model, key('G'))
	if cmd == nil || model.entryCursor != 2 || !model.entries[0].Read ||
		model.entries[1].Read || model.entries[2].Read {
		t.Fatalf("jump result: cursor=%d entries=%#v cmd=%v", model.entryCursor, model.entries, cmd)
	}
	_ = cmd()
	if !reflect.DeepEqual(store.readIDs, []int64{10}) {
		t.Fatalf("marked IDs = %v, want [10]", store.readIDs)
	}
}

func TestReaderMarksReadOnlyAfterBottomAndReturn(t *testing.T) {
	model, store := loadedModel(t)
	model.entries[0].Text = strings.Repeat("long article line\n", 100)
	model.active = articlesPane
	model.height = 12

	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if model.readerReachedBottom {
		t.Fatal("long article opened at bottom")
	}
	model, cmd := update(t, model, key('h'))
	if cmd == nil || len(store.readCalls) != 0 {
		t.Fatal("returning before the bottom did not schedule a progress-only write")
	}
	_ = cmd()
	if len(store.readCalls) != 0 || !reflect.DeepEqual(store.progressIDs, []int64{10}) {
		t.Fatalf("early return writes: progress=%v reads=%v", store.progressIDs, store.readIDs)
	}

	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model, _ = update(t, model, key('G'))
	if !model.readerReachedBottom {
		t.Fatal("reaching the bottom was not latched")
	}
	model, _ = update(t, model, key('k'))
	if model.reader.AtBottom() || !model.readerReachedBottom {
		t.Fatal("scrolling upward cleared the bottom latch")
	}
	model, cmd = update(t, model, key('h'))
	if cmd == nil || !model.entries[0].Read || model.readerEntry == nil || !model.readerEntry.Read {
		t.Fatal("returning after the bottom did not mark the article read")
	}
	_ = cmd()
	if !reflect.DeepEqual(store.readIDs, []int64{10}) {
		t.Fatalf("marked IDs = %v, want [10]", store.readIDs)
	}
}

func TestShiftTabMarksReadOnlyWhenItLeavesReader(t *testing.T) {
	model, store := loadedModel(t)
	model.active = articlesPane
	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	model, cmd := update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if model.active != articlesPane || cmd == nil {
		t.Fatalf("shift-tab did not leave reader and schedule read: active=%d cmd=%v", model.active, cmd)
	}
	_ = cmd()
	if len(store.readCalls) != 1 {
		t.Fatalf("read writes = %d, want 1", len(store.readCalls))
	}
}

func TestShortReaderArticleStartsAtBottom(t *testing.T) {
	model, store := loadedModel(t)
	model.active = articlesPane

	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !model.readerReachedBottom {
		t.Fatal("short article did not begin at the bottom")
	}
	model, cmd := update(t, model, key('h'))
	if cmd == nil {
		t.Fatal("returning from a short article did not schedule a read write")
	}
	_ = cmd()
	if !reflect.DeepEqual(store.readIDs, []int64{10}) {
		t.Fatalf("marked IDs = %v, want [10]", store.readIDs)
	}
}

func TestReaderProgressIsSavedAndRestored(t *testing.T) {
	model, store := loadedModel(t)
	longText := strings.Repeat("long article line\n", 100)
	model.entries[0].Text = longText
	store.entries[0].Text = longText
	model.active = articlesPane
	model.height = 12

	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model, _ = update(t, model, ctrlKey('f'))
	savedOffset := model.reader.YOffset()
	savedProgress := model.reader.ScrollPercent()
	if savedOffset == 0 || savedProgress <= 0 || savedProgress >= 1 {
		t.Fatalf("page down did not reach an intermediate position: offset=%d progress=%f", savedOffset, savedProgress)
	}

	model, cmd := update(t, model, key('h'))
	if cmd == nil {
		t.Fatal("leaving the reader did not schedule progress persistence")
	}
	_ = cmd()
	if !reflect.DeepEqual(store.progressIDs, []int64{10}) ||
		!reflect.DeepEqual(store.progressCalls, []float64{savedProgress}) {
		t.Fatalf("progress writes: ids=%v values=%v", store.progressIDs, store.progressCalls)
	}

	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := model.reader.YOffset(); got != savedOffset {
		t.Fatalf("restored offset = %d, want %d", got, savedOffset)
	}
}

func TestUnreadOnlyReloadPreservesSelectedArticle(t *testing.T) {
	model, store := loadedModel(t)
	store.persistRead = true
	model.SetMarkReadOnScroll(true)
	model.active = articlesPane
	model.filter.UnreadOnly = true

	model, writeCmd := update(t, model, key('j'))
	model, loadCmd := update(t, model, writeCmd())
	if loadCmd == nil {
		t.Fatal("read completion did not reload counts and entries")
	}
	model, _ = update(t, model, loadCmd())
	if len(model.entries) != 1 || model.entries[0].ID != 11 || model.entryCursor != 0 {
		t.Fatalf("reloaded selection: cursor=%d entries=%#v", model.entryCursor, model.entries)
	}
}

func TestHideReadSetsInitialFilterAndCanBeToggledForSession(t *testing.T) {
	store := &fakeStore{
		feeds: []domain.Feed{{ID: 1, Title: "Feed"}},
		entries: []domain.Entry{
			{ID: 10, FeedID: 1, Title: "Unread"},
			{ID: 11, FeedID: 1, Title: "Read", Read: true},
		},
	}
	model := New(store, fakeRefresher{}, func(string) error { return nil })
	model.SetHideRead(true)

	loadCmd := model.Init()
	model, _ = update(t, model, loadCmd())
	if !store.lastFilter.UnreadOnly || len(model.entries) != 1 || model.entries[0].ID != 10 {
		t.Fatalf("initial filter=%#v entries=%#v", store.lastFilter, model.entries)
	}

	model, loadCmd = update(t, model, key('u'))
	if loadCmd == nil || model.filter.UnreadOnly || model.status != "Showing read articles" {
		t.Fatalf("toggle result: filter=%#v status=%q cmd=%v", model.filter, model.status, loadCmd)
	}
	model, _ = update(t, model, loadCmd())
	if store.lastFilter.UnreadOnly || len(model.entries) != 2 {
		t.Fatalf("toggled filter=%#v entries=%#v", store.lastFilter, model.entries)
	}
}

func TestReadPersistenceFailureReloadsAndDuplicateWritesAreSuppressed(t *testing.T) {
	model, store := loadedModel(t)
	store.readErr = errors.New("write failed")
	model.SetMarkReadOnScroll(true)
	model.active = articlesPane

	model, writeCmd := update(t, model, key('j'))
	if writeCmd == nil || !model.entries[0].Read {
		t.Fatal("read state was not updated optimistically")
	}
	if duplicate := model.setRead(10, true); duplicate != nil {
		t.Fatal("already-read entry scheduled a duplicate write")
	}
	model, loadCmd := update(t, model, writeCmd())
	if loadCmd == nil || !model.errStatus || !strings.Contains(model.status, "write failed") {
		t.Fatalf("failure status=%q error=%t cmd=%v", model.status, model.errStatus, loadCmd)
	}
	model, _ = update(t, model, loadCmd())
	if model.entries[0].Read {
		t.Fatal("reload after persistence failure did not restore stored read state")
	}
	if len(store.readCalls) != 1 {
		t.Fatalf("read writes = %d, want 1", len(store.readCalls))
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

func TestPasteIntoInputOverlays(t *testing.T) {
	tests := []struct {
		name string
		mode overlay
	}{
		{name: "add feed", mode: addOverlay},
		{name: "article search", mode: searchOverlay},
		{name: "feed filter", mode: feedFilterOverlay},
		{name: "reader search", mode: readerSearchOverlay},
		{name: "OPML import", mode: importOverlay},
		{name: "OPML export", mode: exportOverlay},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, _ := loadedModel(t)
			next, _ := model.openInput(tt.mode, "Input", "value")
			model = next.(Model)
			model.input.SetValue("leftright")
			model.input.SetCursor(len("left"))

			model, _ = update(t, model, tea.PasteMsg{Content: "feed\nurl\tvalue"})

			if model.overlay != tt.mode {
				t.Fatalf("paste closed overlay %v", model.overlay)
			}
			if got, want := model.input.Value(), "leftfeed url valueright"; got != want {
				t.Fatalf("pasted value = %q, want %q", got, want)
			}
		})
	}
}

func TestPasteIsIgnoredOutsideInputOverlays(t *testing.T) {
	model, _ := loadedModel(t)
	model.input.SetValue("unchanged")
	model.overlay = helpOverlay

	model, cmd := update(t, model, tea.PasteMsg{Content: "pasted"})

	if cmd != nil || model.overlay != helpOverlay || model.input.Value() != "unchanged" {
		t.Fatalf("paste changed non-input overlay: overlay=%v value=%q cmd=%v", model.overlay, model.input.Value(), cmd)
	}
}

func TestPastedFeedURLCanBeSubmitted(t *testing.T) {
	model, store := loadedModel(t)
	store.addFeed = domain.Feed{ID: 42, Title: "Pasted Feed"}
	model, _ = update(t, model, key('a'))
	model, _ = update(t, model, tea.PasteMsg{Content: "  https://example.test/feed.xml\n"})

	model, cmd := update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("submitting a pasted feed URL returned no command")
	}
	if model.overlay != noOverlay || !model.busy {
		t.Fatalf("submitted model: overlay=%v busy=%t", model.overlay, model.busy)
	}

	message := cmd()
	if !reflect.DeepEqual(store.addURLs, []string{"https://example.test/feed.xml"}) {
		t.Fatalf("AddFeed URLs = %q", store.addURLs)
	}
	if added, ok := message.(addMsg); !ok || added.feed.ID != 42 || added.err != nil {
		t.Fatalf("add command returned %#v", message)
	}
}

func TestCtrlVPasteStartsClipboardCommand(t *testing.T) {
	model, _ := loadedModel(t)
	model, _ = update(t, model, key('a'))

	_, cmd := update(t, model, ctrlKey('v'))
	if cmd == nil {
		t.Fatal("ctrl+v returned no clipboard command")
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

func TestConfirmedQuitSavesOpenReaderProgress(t *testing.T) {
	model, store := loadedModel(t)
	model.entries[0].Text = strings.Repeat("long article line\n", 100)
	model.active = articlesPane
	model.height = 12

	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model, _ = update(t, model, ctrlKey('f'))
	wantProgress := model.reader.ScrollPercent()
	model, _ = update(t, model, key('q'))
	model, cmd := update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("confirming quit returned no command")
	}
	if message := cmd(); message == nil {
		t.Fatal("confirming quit returned no message")
	} else if _, ok := message.(tea.QuitMsg); !ok {
		t.Fatalf("confirming quit returned %T, want tea.QuitMsg", message)
	}
	if !reflect.DeepEqual(store.progressIDs, []int64{10}) ||
		!reflect.DeepEqual(store.progressCalls, []float64{wantProgress}) {
		t.Fatalf("quit progress writes: ids=%v values=%v", store.progressIDs, store.progressCalls)
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

func TestReaderFooterShowsAndUpdatesPercentageRead(t *testing.T) {
	model, _ := loadedModel(t)
	model.entries[0].Text = strings.Repeat("line\n", 40)
	model.active = articlesPane
	model.height = 12

	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	footer := strings.Split(ansi.Strip(model.View().Content), "\n")
	if got := footer[len(footer)-1]; !strings.HasSuffix(got, "0% read") {
		t.Fatalf("reader footer at top = %q, want 0%% read", got)
	}

	model, _ = update(t, model, ctrlKey('f'))
	want := fmt.Sprintf("%.0f%% read", model.reader.ScrollPercent()*100)
	footer = strings.Split(ansi.Strip(model.View().Content), "\n")
	if got := footer[len(footer)-1]; !strings.HasSuffix(got, want) || want == "0% read" || want == "100% read" {
		t.Fatalf("reader footer after page down = %q, want intermediate %q", got, want)
	}

	model, _ = update(t, model, key('G'))
	footer = strings.Split(ansi.Strip(model.View().Content), "\n")
	if got := footer[len(footer)-1]; !strings.HasSuffix(got, "100% read") {
		t.Fatalf("reader footer at bottom = %q, want 100%% read", got)
	}
}

func TestReaderFooterShowsFullyReadWhenArticleFits(t *testing.T) {
	model, _ := loadedModel(t)
	model.active = articlesPane

	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	footer := strings.Split(ansi.Strip(model.View().Content), "\n")
	if got := footer[len(footer)-1]; !strings.HasSuffix(got, "100% read") {
		t.Fatalf("short article footer = %q, want 100%% read", got)
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
