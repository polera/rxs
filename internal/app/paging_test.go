package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/store"
)

func testPagedModel(t *testing.T) Model {
	t.Helper()
	repository, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	feed, err := repository.AddFeed(context.Background(), "https://example.test/feed")
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]domain.Entry, 135)
	for i := range entries {
		entries[i] = domain.Entry{
			Identity: fmt.Sprint(i), Title: fmt.Sprintf("Article %d", i),
			URL:  fmt.Sprintf("https://example.test/%d", i),
			HTML: "<p>body</p>", Text: "body",
			PublishedAt: time.Date(2026, 1, 1, 0, 0, i/3, 0, time.UTC),
		}
	}
	if _, err := repository.ApplyRefresh(context.Background(), feed.ID, domain.ParsedFeed{Entries: entries}); err != nil {
		t.Fatal(err)
	}
	model := New(repository, fakeRefresher{}, nil)
	model, _ = update(t, model, model.loadCmd()())
	model.active = articlesPane
	return model
}

func TestPagedNavigationAndReader(t *testing.T) {
	m := testPagedModel(t)
	if len(m.entries) != entryPageSize || !m.hasNext || m.hasPrevious || m.selectedEntryID() != 135 {
		t.Fatalf("initial page: %d rows, next=%v previous=%v selected=%d", len(m.entries), m.hasNext, m.hasPrevious, m.selectedEntryID())
	}
	for range entryPageSize - 1 {
		m, _ = update(t, m, key('j'))
	}
	if m.selectedEntryID() != 72 {
		t.Fatalf("end of first page: %d", m.selectedEntryID())
	}
	next, pageCmd := update(t, m, key('j'))
	if pageCmd == nil || !next.pageLoading || next.selectedEntryID() != 72 {
		t.Fatalf("pending next page: selected=%d loading=%v", next.selectedEntryID(), next.pageLoading)
	}
	m, _ = update(t, next, pageCmd())
	if m.selectedEntryID() != 71 || !m.hasPrevious || !m.hasNext {
		t.Fatalf("second page: selected=%d previous=%v next=%v", m.selectedEntryID(), m.hasPrevious, m.hasNext)
	}
	next, pageCmd = update(t, m, key('k'))
	m, _ = update(t, next, pageCmd())
	if m.selectedEntryID() != 72 || m.hasPrevious {
		t.Fatalf("previous page: selected=%d previous=%v", m.selectedEntryID(), m.hasPrevious)
	}
	next, pageCmd = update(t, m, key('G'))
	m, _ = update(t, next, pageCmd())
	if m.selectedEntryID() != 1 || m.hasNext || !m.hasPrevious {
		t.Fatalf("last page: selected=%d previous=%v next=%v", m.selectedEntryID(), m.hasPrevious, m.hasNext)
	}
	m, _ = update(t, m, key('g'))
	next, pageCmd = update(t, m, key('g'))
	m, _ = update(t, next, pageCmd())
	if m.selectedEntryID() != 135 || m.hasPrevious {
		t.Fatalf("first page: selected=%d previous=%v", m.selectedEntryID(), m.hasPrevious)
	}
	if !m.entries[0].Unloaded || m.entries[0].HTML != "" {
		t.Fatalf("list should contain metadata only: %#v", m.entries[0])
	}
	m, bodyCmd := update(t, m, key('l'))
	if m.readerEntry == nil || !m.readerEntry.Unloaded || !strings.Contains(m.reader.View(), "Loading article") || bodyCmd == nil {
		t.Fatalf("reader not awaiting body: %#v", m.readerEntry)
	}
	m, _ = update(t, m, bodyCmd())
	if m.readerEntry.Unloaded || m.readerEntry.Text != "body" || !strings.Contains(m.reader.View(), "body") {
		t.Fatalf("reader did not hydrate: %#v", m.readerEntry)
	}
}

func TestPagedSearchAndStalePreview(t *testing.T) {
	m := testPagedModel(t)
	m.width = 120
	m.filter.Search = "Article 1"
	m.resetPage()
	m, oldCmd := update(t, m, m.loadCmd()())
	if len(m.entries) != 46 || m.hasNext {
		t.Fatalf("search result: %d rows, next=%v", len(m.entries), m.hasNext)
	}
	if oldCmd == nil {
		t.Fatal("expected preview load")
	}
	m, newCmd := update(t, m, key('j'))
	m, _ = update(t, m, oldCmd())
	if !m.entries[m.entryCursor].Unloaded {
		t.Fatal("stale preview replaced current article")
	}
	m, _ = update(t, m, newCmd())
	if m.entries[m.entryCursor].Unloaded || m.entries[m.entryCursor].Text != "body" {
		t.Fatalf("current preview not hydrated: %#v", m.entries[m.entryCursor])
	}
	m, _ = update(t, m, key('x'))
	if m.filter.Search != "" || m.page != (pageRequest{}) {
		t.Fatalf("clearing search did not reset page: %#v %#v", m.filter, m.page)
	}
}

func TestPagedUnreadAndPreviewState(t *testing.T) {
	m := testPagedModel(t)
	m.markReadOnScroll = true
	for range entryPageSize - 1 {
		next, cmd := update(t, m, key('j'))
		m = next
		if cmd != nil {
			m, _ = update(t, m, primaryCommandMessage(t, cmd))
		}
	}
	if m.selectedEntryID() != 72 {
		t.Fatalf("boundary article: %d", m.selectedEntryID())
	}
	next, pageCmd := update(t, m, key('j'))
	m, stateCmd := update(t, next, pageCmd())
	if m.selectedEntryID() != 71 || stateCmd == nil {
		t.Fatalf("crossing page: article=%d command=%v", m.selectedEntryID(), stateCmd)
	}
	m, reload := update(t, m, primaryCommandMessage(t, stateCmd))
	if reload != nil {
		m, _ = update(t, m, primaryCommandMessage(t, reload))
	}
	previous, err := m.store.(*store.Store).Entry(context.Background(), 72)
	if err != nil || !previous.Read {
		t.Fatalf("previous article not marked read: %#v %v", previous, err)
	}
	m.markReadOnScroll = false
	m.filter.UnreadOnly = true
	m.resetPage()
	m, _ = update(t, m, m.loadCmd()())
	if m.hasPrevious || len(m.entries) != entryPageSize || m.entries[0].ID >= 135 {
		t.Fatalf("unread page: first=%d count=%d previous=%v", m.entries[0].ID, len(m.entries), m.hasPrevious)
	}
}

func TestReaderExitBeforeBodyDoesNotSavePlaceholderProgress(t *testing.T) {
	m := testPagedModel(t)
	m, bodyCmd := update(t, m, key('l'))
	if bodyCmd == nil {
		t.Fatal("expected reader hydration")
	}
	m, stateCmd := update(t, m, key('h'))
	if stateCmd != nil || m.active != articlesPane {
		t.Fatalf("reader exit queued placeholder progress: %v, pane=%v", stateCmd, m.active)
	}
	m, _ = update(t, m, bodyCmd())
	entry, err := m.store.(*store.Store).Entry(context.Background(), 135)
	if err != nil || entry.ReadingProgress != 0 || entry.Read {
		t.Fatalf("placeholder persisted reading progress: %#v %v", entry, err)
	}
}

func TestPageLoadPreservesSelectionAndRejectsOldFilter(t *testing.T) {
	m := testPagedModel(t)
	for range entryPageSize - 1 {
		m, _ = update(t, m, key('j'))
	}
	next, pageCmd := update(t, m, key('j'))
	m, _ = update(t, next, pageCmd())
	m, _ = update(t, m, key('j'))
	selected := m.selectedEntryID()
	if selected != 70 {
		t.Fatalf("expected second-page selection, got %d", selected)
	}
	// A refresh can add entries before the current cursor without moving the
	// reader or changing which article is selected.
	repository := m.store.(*store.Store)
	feed, err := repository.Feeds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ApplyRefresh(context.Background(), feed[0].ID, domain.ParsedFeed{Entries: []domain.Entry{{
		Identity: "new", Title: "Newest", HTML: "<p>new</p>",
		PublishedAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	}}}); err != nil {
		t.Fatal(err)
	}
	m, _ = update(t, m, m.loadCmdPreserving()())
	if m.selectedEntryID() != selected {
		t.Fatalf("refresh moved selection: %d", m.selectedEntryID())
	}
	oldLoad := m.loadCmdPreserving()
	m.filter.Search = "Newest"
	m.resetPage()
	m, _ = update(t, m, m.loadCmd()())
	if m.selectedEntryID() != 136 {
		t.Fatalf("search did not select new article: %d", m.selectedEntryID())
	}
	m, _ = update(t, m, oldLoad())
	if m.selectedEntryID() != 136 || len(m.entries) != 1 {
		t.Fatalf("stale page replaced search result: %#v", m.entries)
	}
}
