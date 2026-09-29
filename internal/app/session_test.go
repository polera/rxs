package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/store"
)

func TestRestoresOlderReadArticleAndKeepsSelectionAfterRefresh(t *testing.T) {
	m := testPagedModel(t)
	repository := m.store.(*store.Store)
	ctx := context.Background()
	feed := m.allFeeds[0]
	if _, err := repository.ApplyRefresh(ctx, feed.ID, domain.ParsedFeed{Entries: []domain.Entry{{
		Identity: "11", Title: "Article 11", HTML: strings.Repeat("<p>Many lines of article text.</p>", 120),
		PublishedAt: time.Date(2026, 1, 1, 0, 0, 11/3, 0, time.UTC),
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetRead(ctx, 12, true); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetReadingProgress(ctx, 12, 0.6); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveLastView(ctx, domain.LastView{Pane: "reader", Scope: "feed", FeedID: feed.ID, EntryID: 12}); err != nil {
		t.Fatal(err)
	}
	model := New(repository, fakeRefresher{}, nil)
	model.SetHideRead(true)
	model.SetResumeLastView(true)
	model, restore := update(t, model, model.Init()())
	if restore == nil || !model.resuming {
		t.Fatal("initial load did not schedule restoration")
	}
	model, _ = update(t, model, primaryCommandMessage(t, restore))
	if model.active != readerPane || model.readerEntry == nil || model.readerEntry.ID != 12 ||
		model.selectedEntryID() != 12 || model.filter.UnreadOnly || !model.hasPrevious || model.hasNext {
		t.Fatalf("restored reader: pane=%d entry=%#v selected=%d filter=%#v prev=%t next=%t",
			model.active, model.readerEntry, model.selectedEntryID(), model.filter, model.hasPrevious, model.hasNext)
	}
	model, _ = update(t, model, model.loadBodyCmd()())
	if model.readerEntry.Unloaded || model.reader.ScrollPercent() < 0.3 {
		t.Fatalf("reader did not resume saved progress: unloaded=%t progress=%f", model.readerEntry.Unloaded, model.reader.ScrollPercent())
	}
	if _, err := repository.ApplyRefresh(ctx, feed.ID, domain.ParsedFeed{Entries: []domain.Entry{{
		Identity: "new", PublishedAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	}}}); err != nil {
		t.Fatal(err)
	}
	model, _ = update(t, model, model.loadCmdPreserving()())
	if model.selectedEntryID() != 12 || model.readerEntry.ID != 12 {
		t.Fatalf("refresh moved resumed article: selected=%d reader=%d", model.selectedEntryID(), model.readerEntry.ID)
	}
}

func TestRestoreMissingArticleFallsBackToFeed(t *testing.T) {
	m := testPagedModel(t)
	repository := m.store.(*store.Store)
	feed := m.allFeeds[0]
	if err := repository.SaveLastView(context.Background(), domain.LastView{Pane: "reader", Scope: "feed", FeedID: feed.ID, EntryID: 9999}); err != nil {
		t.Fatal(err)
	}
	model := New(repository, fakeRefresher{}, nil)
	model.SetResumeLastView(true)
	model, restore := update(t, model, model.Init()())
	model, _ = update(t, model, primaryCommandMessage(t, restore))
	if model.active != articlesPane || model.filter.FeedID != feed.ID || model.readerEntry != nil || model.selectedEntryID() != 135 {
		t.Fatalf("missing article fallback: pane=%d filter=%#v selected=%d", model.active, model.filter, model.selectedEntryID())
	}
}

func TestRestoresFeedPaneAndOlderPreview(t *testing.T) {
	m := testPagedModel(t)
	repository := m.store.(*store.Store)
	feed := m.allFeeds[0]
	if err := repository.SaveLastView(context.Background(), domain.LastView{Pane: "feeds", Scope: "feed", FeedID: feed.ID, EntryID: 12}); err != nil {
		t.Fatal(err)
	}
	model := New(repository, fakeRefresher{}, nil)
	model.SetResumeLastView(true)
	model, restore := update(t, model, model.Init()())
	model, _ = update(t, model, primaryCommandMessage(t, restore))
	if model.active != feedsPane || model.feedCursor != 2 || model.selectedEntryID() != 12 || !model.hasPrevious {
		t.Fatalf("feed preview: pane=%d feed=%d article=%d prev=%t", model.active, model.feedCursor, model.selectedEntryID(), model.hasPrevious)
	}
}

type sessionStore struct {
	*fakeStore
	view      domain.LastView
	saveErr   error
	saveCalls int
	loadCalls int
}

func (s *sessionStore) LoadLastView(context.Context) (domain.LastView, bool, error) {
	s.loadCalls++
	return s.view, true, nil
}

func (s *sessionStore) SaveLastView(_ context.Context, view domain.LastView) error {
	s.saveCalls++
	if s.saveErr != nil {
		return s.saveErr
	}
	s.view = view
	return nil
}

func TestConfirmedQuitSavesViewAfterProgressAndRetriesFailure(t *testing.T) {
	m, base := loadedModel(t)
	s := &sessionStore{fakeStore: base, saveErr: errors.New("session write failed")}
	m.store = s
	m.SetResumeLastView(true)
	m.active = articlesPane
	m, _ = update(t, m, key('l'))
	m, _ = update(t, m, key('q'))
	m, progress := update(t, m, key('y'))
	if progress == nil || s.saveCalls != 0 || len(s.progressCalls) != 0 {
		t.Fatal("quit wrote the view before reading progress")
	}
	m, save := update(t, m, progress())
	if save == nil || s.saveCalls != 0 || len(s.progressCalls) != 1 {
		t.Fatal("progress did not finish before the view write")
	}
	m, _ = update(t, m, save())
	if m.quitting || m.overlay != noOverlay || !strings.Contains(m.status, "session write failed") {
		t.Fatalf("failed save did not keep UI open: quitting=%t status=%q", m.quitting, m.status)
	}
	s.saveErr = nil
	m, _ = update(t, m, key('q'))
	m, progress = update(t, m, key('y'))
	m, save = update(t, m, progress())
	_, quit := update(t, m, save())
	if _, ok := quit().(tea.QuitMsg); !ok || s.view.Pane != "reader" || s.view.EntryID != 10 || s.saveCalls != 2 {
		t.Fatalf("retry did not save reader view: %#v calls=%d", s.view, s.saveCalls)
	}
}

func TestUnexpectedShutdownFlushesStateBeforeView(t *testing.T) {
	m, base := loadedModel(t)
	s := &sessionStore{fakeStore: base}
	m.store = s
	m.SetResumeLastView(true)
	m.active = articlesPane
	m, _ = update(t, m, key('s')) // Leave the state command deferred.
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.starCalls) != 1 || !s.starCalls[0] || s.view.Pane != "articles" || s.view.EntryID != 10 {
		t.Fatalf("shutdown state/view: star=%v, view=%#v", s.starCalls, s.view)
	}
}

func TestDisabledResumeDoesNotReadOrSaveView(t *testing.T) {
	s := &sessionStore{fakeStore: &fakeStore{feeds: []domain.Feed{{ID: 1}},
		entries: []domain.Entry{{ID: 10, FeedID: 1}}},
		view: domain.LastView{Pane: "reader", Scope: "feed", FeedID: 1, EntryID: 10}}
	m := New(s, fakeRefresher{}, nil)
	m, _ = update(t, m, m.Init()())
	if m.active != feedsPane || s.loadCalls != 0 {
		t.Fatalf("disabled restoration: pane=%d reads=%d", m.active, s.loadCalls)
	}
	m, _ = update(t, m, key('q'))
	_, quit := update(t, m, key('y'))
	if _, ok := quit().(tea.QuitMsg); !ok || s.saveCalls != 0 {
		t.Fatalf("disabled quit: writes=%d", s.saveCalls)
	}
}

func TestRestoreFeedSelectionAndMissingFeedFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		view   domain.LastView
		feedID int64
		cursor int
	}{
		{"feed", domain.LastView{Pane: "feeds", Scope: "feed", FeedID: 1}, 1, 2},
		{"starred", domain.LastView{Pane: "feeds", Scope: "starred"}, 0, 1},
		{"deleted", domain.LastView{Pane: "feeds", Scope: "feed", FeedID: 99}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &sessionStore{fakeStore: &fakeStore{feeds: []domain.Feed{{ID: 1}}}, view: tc.view}
			m := New(s, fakeRefresher{}, nil)
			m.SetResumeLastView(true)
			m, restore := update(t, m, m.Init()())
			m, _ = update(t, m, primaryCommandMessage(t, restore))
			if m.active != feedsPane || m.feedCursor != tc.cursor || m.filter.FeedID != tc.feedID ||
				m.filter.StarredOnly != (tc.name == "starred") {
				t.Fatalf("feed fallback: pane=%d cursor=%d filter=%#v", m.active, m.feedCursor, m.filter)
			}
			m, _ = update(t, m, key('q'))
			m, save := update(t, m, key('y'))
			_, quit := update(t, m, save())
			if _, ok := quit().(tea.QuitMsg); !ok || s.view.FeedID != tc.feedID {
				t.Fatalf("saved feed view: %#v", s.view)
			}
		})
	}
}

func TestFailedProgressAndCanceledQuitDoNotSaveView(t *testing.T) {
	m, base := loadedModel(t)
	s := &sessionStore{fakeStore: base}
	m.store = s
	m.SetResumeLastView(true)
	m.active = articlesPane
	m, _ = update(t, m, key('l'))
	m, _ = update(t, m, key('q'))
	m, _ = update(t, m, key('n'))
	if s.saveCalls != 0 || m.overlay != noOverlay {
		t.Fatal("canceling quit saved the view")
	}
	s.progressErr = errors.New("progress failed")
	m, _ = update(t, m, key('q'))
	m, progress := update(t, m, key('y'))
	m, _ = update(t, m, progress())
	if s.saveCalls != 0 || m.quitting || !strings.Contains(m.status, "progress failed") {
		t.Fatalf("failed progress saved view: saves=%d status=%q", s.saveCalls, m.status)
	}
}

func TestShutdownFlushesConfirmedViewOnlyAfterSuccessfulState(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "state_failure"}[failed], func(t *testing.T) {
			m, base := loadedModel(t)
			s := &sessionStore{fakeStore: base}
			m.store = s
			m.SetResumeLastView(true)
			m.active = articlesPane
			m, _ = update(t, m, key('l'))
			if failed {
				s.progressErr = errors.New("progress failed")
			}
			m, _ = update(t, m, key('q'))
			m, deferred := update(t, m, key('y'))
			err := m.Shutdown(context.Background())
			if (err != nil) != failed || s.saveCalls != map[bool]int{false: 1, true: 0}[failed] {
				t.Fatalf("shutdown: err=%v, view saves=%d", err, s.saveCalls)
			}
			if deferred() != nil {
				t.Fatal("deferred write ran after shutdown")
			}
		})
	}
}
