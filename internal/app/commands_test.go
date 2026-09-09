package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/opml"
)

func TestReaderActionsTargetSnapshotAfterReload(t *testing.T) {
	for _, empty := range []bool{false, true} {
		for _, action := range []rune{'o', 'y', 's', ' '} {
			t.Run(fmt.Sprintf("empty=%t/action=%q", empty, action), func(t *testing.T) {
				model, store := loadedModel(t)
				model.entries[0].URL = "https://example.test/first"
				model.entries[1].URL = "https://example.test/second"
				model.entries[0].HTML = `<a href="/link">Link</a>`
				var opened string
				model.browser = func(url string) error { opened = url; return nil }
				model.active = articlesPane
				model, _ = update(t, model, key('l'))
				entries := append([]domain.Entry(nil), model.entries[1:]...)
				if empty {
					entries = nil
				}
				model, _ = update(t, model, loadedMsg{feeds: model.allFeeds, entries: entries})
				model, cmd := update(t, model, key(action))
				message := primaryCommandMessage(t, cmd)
				switch action {
				case 'o':
					if opened != "https://example.test/first" {
						t.Fatalf("opened %q", opened)
					}
				case 'y':
					if fmt.Sprint(message) != "https://example.test/first" {
						t.Fatalf("copied %v", message)
					}
				case 's':
					if !reflect.DeepEqual(store.starIDs, []int64{10}) || !model.readerEntry.Starred {
						t.Fatalf("star IDs=%v snapshot=%#v", store.starIDs, model.readerEntry)
					}
				case ' ':
					if !reflect.DeepEqual(store.readIDs, []int64{10}) || !model.readerEntry.Read {
						t.Fatalf("read IDs=%v snapshot=%#v", store.readIDs, model.readerEntry)
					}
				}
			})
		}
	}
}

func TestArticleListActionsIgnoreOldReaderSnapshot(t *testing.T) {
	model, _ := loadedModel(t)
	model.entries[0].URL = "https://example.test/first"
	model.entries[1].URL = "https://example.test/second"
	model.active = articlesPane
	model, _ = update(t, model, key('l'))
	model, _ = update(t, model, loadedMsg{feeds: model.allFeeds, entries: model.entries[1:]})
	model, _ = update(t, model, key('h'))
	model, cmd := update(t, model, key('y'))
	if got := fmt.Sprint(primaryCommandMessage(t, cmd)); got != "https://example.test/second" {
		t.Fatalf("copied %q", got)
	}
}

func TestReaderEnterPreservesSnapshotAndOpensLinksWithEmptyList(t *testing.T) {
	model, _ := loadedModel(t)
	model.entries[0].URL = "https://example.test/first"
	model.entries[0].HTML = `<a href="/related">Related</a>`
	model.active = articlesPane
	model, _ = update(t, model, key('l'))
	model, _ = update(t, model, loadedMsg{feeds: model.allFeeds})
	model, cmd := update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd != nil || model.readerEntry == nil || model.readerEntry.ID != 10 {
		t.Fatal("Enter changed the tracked reader article")
	}
	var opened string
	model.browser = func(url string) error { opened = url; return nil }
	model, _ = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	_, cmd = update(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	_ = primaryCommandMessage(t, cmd)
	if opened != "https://example.test/related" {
		t.Fatalf("opened %q", opened)
	}
}

type gatedStateStore struct {
	*fakeStore
	started chan stateFields
	release chan struct{}
}

func (s *gatedStateStore) SetRead(ctx context.Context, id int64, value bool) error {
	s.started <- readField
	<-s.release
	return s.fakeStore.SetRead(ctx, id, value)
}

func (s *gatedStateStore) SetStarred(ctx context.Context, id int64, value bool) error {
	s.started <- starredField
	<-s.release
	return s.fakeStore.SetStarred(ctx, id, value)
}

func TestStateWritesWaitForPreviousCompletion(t *testing.T) {
	for _, action := range []rune{' ', 's'} {
		t.Run(fmt.Sprintf("%q", action), func(t *testing.T) {
			model, store := loadedModel(t)
			store.persistRead = true
			gate := &gatedStateStore{fakeStore: store, started: make(chan stateFields, 2), release: make(chan struct{})}
			release := sync.OnceFunc(func() { close(gate.release) })
			t.Cleanup(release)
			model.store = gate
			model.active = articlesPane
			model, first := update(t, model, key(action))
			completed := make(chan tea.Msg, 1)
			go func() { completed <- first() }()
			select {
			case <-gate.started:
			case <-time.After(5 * time.Second):
				t.Fatal("first write did not start")
			}
			model, second := update(t, model, key(action))
			if second != nil || len(model.stateWrites) != 2 {
				t.Fatal("second write was launched before the first completion")
			}
			release()
			var message tea.Msg
			select {
			case message = <-completed:
			case <-time.After(5 * time.Second):
				t.Fatal("first write did not finish")
			}
			model, second = update(t, model, message)
			if second == nil || model.entries[0].Read || model.entries[0].Starred {
				t.Fatal("older completion overwrote the latest optimistic intention")
			}
			model, _ = update(t, model, second())
			if len(model.stateWrites) != 0 || store.entries[0].Read || store.entries[0].Starred {
				t.Fatalf("final state=%#v pending=%d", store.entries[0], len(model.stateWrites))
			}
			calls := store.readCalls
			if action == 's' {
				calls = store.starCalls
			}
			if !reflect.DeepEqual(calls, []bool{true, false}) {
				t.Fatalf("writes=%v", calls)
			}
		})
	}
}

func TestReaderStateFailureRestoresConfirmedState(t *testing.T) {
	for _, action := range []rune{' ', 's'} {
		t.Run(fmt.Sprintf("%q", action), func(t *testing.T) {
			model, store := loadedModel(t)
			store.readErr, store.starErr = errors.New("read failed"), errors.New("star failed")
			model.active = articlesPane
			model, _ = update(t, model, key('l'))
			model, cmd := update(t, model, key(action))
			model, _ = update(t, model, cmd())
			if !model.errStatus || model.readerEntry.Read || model.readerEntry.Starred {
				t.Fatalf("failed snapshot=%#v status=%q", model.readerEntry, model.status)
			}
			model, cmd = update(t, model, key(action))
			model, _ = update(t, model, cmd())
			calls := store.readCalls
			if action == 's' {
				calls = store.starCalls
			}
			if !reflect.DeepEqual(calls, []bool{true, true}) {
				t.Fatalf("retry writes=%v", calls)
			}
		})
	}
}

func TestReloadReconcilesReaderStateWithoutReplacingContent(t *testing.T) {
	model, _ := loadedModel(t)
	model.active = articlesPane
	model, _ = update(t, model, key('l'))
	entry := model.entries[0]
	entry.Read, entry.Starred, entry.ReadingProgress = true, true, 0.5
	entry.Text = "new article body"
	model, _ = update(t, model, loadedMsg{feeds: model.allFeeds, entries: []domain.Entry{entry}})
	if stateOf(*model.readerEntry) != stateOf(entry) || model.readerEntry.Text != "one" {
		t.Fatalf("reconciled snapshot=%#v", model.readerEntry)
	}
}

func TestFailedQueuedWritesDoNotRestoreFailedOptimisticValues(t *testing.T) {
	model, store := loadedModel(t)
	store.readErr = errors.New("read failed")
	model.active = articlesPane
	model, first := update(t, model, key(' '))
	model, _ = update(t, model, key(' '))
	model, next := update(t, model, first())
	model, _ = update(t, model, primaryCommandMessage(t, next))
	if model.entries[0].Read || len(model.stateWrites) != 0 {
		t.Fatalf("failed writes left entry=%#v pending=%d", model.entries[0], len(model.stateWrites))
	}
}

func TestReadingProgressIsOrderedAcrossSessionsAndQuit(t *testing.T) {
	model, store := loadedModel(t)
	model.entries[0].Text = strings.Repeat("line\n", 100)
	model.active = articlesPane
	model.height = 12
	model, _ = update(t, model, key('l'))
	model, _ = update(t, model, ctrlKey('f'))
	firstProgress := model.reader.ScrollPercent()
	model, first := update(t, model, key('h'))
	model, _ = update(t, model, key('l'))
	model, _ = update(t, model, ctrlKey('f'))
	secondProgress := model.reader.ScrollPercent()
	model, second := update(t, model, key('h'))
	if second != nil || secondProgress <= firstProgress {
		t.Fatalf("second cmd=%v progress=%v -> %v", second, firstProgress, secondProgress)
	}
	model, _ = update(t, model, key('q'))
	model, quit := update(t, model, key('y'))
	if quit != nil || !model.quitting {
		t.Fatal("quit did not wait for pending progress writes")
	}
	model, second = update(t, model, first())
	model, quit = update(t, model, second())
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("queue drain did not quit")
	}
	if !reflect.DeepEqual(store.progressCalls, []float64{firstProgress, secondProgress}) {
		t.Fatalf("progress writes=%v", store.progressCalls)
	}
}

func TestQuitProgressFailureKeepsReaderOpen(t *testing.T) {
	model, store := loadedModel(t)
	store.progressErr = errors.New("progress failed")
	model.active = articlesPane
	model, _ = update(t, model, key('l'))
	model, _ = update(t, model, key('q'))
	model, cmd := update(t, model, key('y'))
	model, _ = update(t, model, cmd())
	if model.quitting || model.overlay != noOverlay || !model.errStatus || model.active != readerPane {
		t.Fatalf("failure: quitting=%t overlay=%v active=%v status=%q", model.quitting, model.overlay, model.active, model.status)
	}
}

func TestLoadGenerationsRejectObsoleteResultsAndErrors(t *testing.T) {
	for _, staleError := range []bool{false, true} {
		t.Run(fmt.Sprintf("error=%t", staleError), func(t *testing.T) {
			model, store := loadedModel(t)
			old := model.loadCmd()().(loadedMsg)
			if staleError {
				old.err = errors.New("obsolete error")
			}
			// Returning to the same filter must not revive the old generation.
			model.filter.Search = "other"
			_ = model.loadCmd()
			model.filter.Search = ""
			store.entries = []domain.Entry{{ID: 12, Title: "Current"}}
			current := model.loadCmd()()
			model, _ = update(t, model, current)
			model, _ = update(t, model, old)
			if model.errStatus || len(model.entries) != 1 || model.entries[0].ID != 12 {
				t.Fatalf("stale result applied: entries=%#v status=%q", model.entries, model.status)
			}
		})
	}
}

func TestPreservingLoadKeepsNavigationMadeAfterScheduling(t *testing.T) {
	model, _ := loadedModel(t)
	model.active = articlesPane
	load := model.loadCmdPreserving()
	model, _ = update(t, model, key('j'))
	model, _ = update(t, model, load())
	if model.selectedEntryID() != 11 {
		t.Fatalf("reload undid navigation: %d", model.selectedEntryID())
	}
}

func TestLoadDuringStateWriteRetainsOptimisticState(t *testing.T) {
	model, store := loadedModel(t)
	store.persistRead = true
	model.active = articlesPane
	old := model.loadCmd()()
	model, write := update(t, model, key(' '))
	model, _ = update(t, model, old)
	model, _ = update(t, model, model.loadCmd()())
	if !model.entries[0].Read {
		t.Fatal("load erased pending read intention")
	}
	model, load := update(t, model, write())
	model, _ = update(t, model, load())
	if !model.entries[0].Read {
		t.Fatal("persisted read state lost")
	}
}

func TestSupersededInitialLoadStillRefreshesOnce(t *testing.T) {
	for _, initialFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("initialFirst=%t", initialFirst), func(t *testing.T) {
			store := &fakeStore{feeds: []domain.Feed{{ID: 1, Title: "Feed"}}}
			refresher := &recordingRefresher{}
			model := New(store, refresher, nil)
			initial := model.Init()()
			model.filter.Search = "new filter"
			current := model.loadCmd()()
			first, second := current, initial
			if initialFirst {
				first, second = initial, current
			}
			model, cmd := update(t, model, first)
			if cmd != nil {
				t.Fatal("refresh began before both initial intent and a current load arrived")
			}
			model, cmd = update(t, model, second)
			if cmd == nil || !model.busy {
				t.Fatal("superseding startup load lost initial refresh")
			}
			model, cmd = update(t, model, cmd())
			model, _ = update(t, model, primaryCommandMessage(t, cmd))
			if refresher.calls != 1 {
				t.Fatalf("refresh calls=%d", refresher.calls)
			}
		})
	}
}

func TestDeleteConfirmationUsesCapturedIdentity(t *testing.T) {
	model, store := loadedModel(t)
	model.feedCursor = 2
	model, _ = update(t, model, key('d'))
	other := domain.Feed{ID: 2, Title: "Other", URL: "https://other.test/feed"}
	model, _ = update(t, model, loadedMsg{feeds: []domain.Feed{other, model.allFeeds[0]}})
	if !strings.Contains(model.overlayView(), `Remove "Feed"`) {
		t.Fatal("reload changed displayed deletion target")
	}
	model, cmd := update(t, model, key('y'))
	message := cmd()
	if !reflect.DeepEqual(store.deleteIDs, []int64{1}) {
		t.Fatalf("deleted IDs=%v", store.deleteIDs)
	}
	model.feedCursor = 2
	model, _ = update(t, model, key('d'))
	if model.overlay != noOverlay {
		t.Fatal("opened a duplicate deletion dialog")
	}
	model, _ = update(t, model, message)
	model, _ = update(t, model, key('y'))
	if len(store.deleteIDs) != 1 {
		t.Fatalf("duplicate deletion: %v", store.deleteIDs)
	}
}

func TestDeleteConfirmationRejectsRemovedTargetAndBusyState(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(fmt.Sprintf("busy=%t", busy), func(t *testing.T) {
			model, store := loadedModel(t)
			model.feedCursor = 2
			model, _ = update(t, model, key('d'))
			if busy {
				model.busy = true
			} else {
				model, _ = update(t, model, loadedMsg{})
			}
			model, _ = update(t, model, key('y'))
			if model.deleting || len(store.deleteIDs) != 0 || model.overlay != noOverlay {
				t.Fatal("invalid target was submitted for deletion")
			}
		})
	}
}

func TestExportIncludesAllSubscriptionsDespiteDisplayFilter(t *testing.T) {
	for _, term := range []string{"Feed", "no matches"} {
		t.Run(term, func(t *testing.T) {
			model, store := loadedModel(t)
			model.allFeeds = append(model.allFeeds, domain.Feed{ID: 2, Title: "Other", URL: "https://other.test/rss"})
			store.feeds = append([]domain.Feed(nil), model.allFeeds...)
			model.feedFilter = term
			model.applyFeedSearch()
			path := filepath.Join(t.TempDir(), "subscriptions.opml")
			if err := os.WriteFile(path, []byte("previous backup"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := model.exportCmd(path)
			model.allFeeds[0].URL = "https://changed.test/feed"
			if msg := cmd().(exportMsg); msg.err != nil {
				t.Fatal(msg.err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			feeds, err := opml.Import(file)
			if err != nil || len(feeds) != 2 || feeds[0].FeedURL != "https://example.test/feed" || feeds[1].FeedURL != "https://other.test/rss" {
				t.Fatalf("exported feeds=%#v error=%v", feeds, err)
			}
		})
	}
}

func TestExportQueriesCompleteStoreAndPreservesBackupOnQueryFailure(t *testing.T) {
	model, store := loadedModel(t)
	model.allFeeds = nil
	store.feeds = append(store.feeds, domain.Feed{ID: 2, Title: "Added before reload", URL: "https://other.test/rss"})
	path := filepath.Join(t.TempDir(), "subscriptions.opml")
	if msg := model.exportCmd(path)().(exportMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	feeds, err := opml.Import(strings.NewReader(string(before)))
	if err != nil || len(feeds) != 2 {
		t.Fatalf("exported incomplete cache: feeds=%#v err=%v", feeds, err)
	}
	store.feedsErr = errors.New("database unavailable")
	if msg := model.exportCmd(path)().(exportMsg); !errors.Is(msg.err, store.feedsErr) {
		t.Fatalf("export error=%v", msg.err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("failed export changed backup: %q err=%v", after, err)
	}
}

func TestEmptyReaderCannotStartAnUntrackedSession(t *testing.T) {
	model := New(&fakeStore{}, fakeRefresher{}, nil)
	model, _ = update(t, model, key('l'))
	model, _ = update(t, model, key('l'))
	if model.active != articlesPane {
		t.Fatal("entered reader without an article")
	}
	model, _ = update(t, model, loadedMsg{entries: []domain.Entry{{ID: 1, URL: "https://example.test/article"}}})
	model, _ = update(t, model, key('l'))
	if model.active != readerPane || model.readerEntry == nil || model.readerEntry.ID != 1 {
		t.Fatal("loaded article did not start a tracked session")
	}
}

func TestRefreshReloadPreservesSelectionAndSupersedesStateReload(t *testing.T) {
	model, store := loadedModel(t)
	model.active = articlesPane
	model.entryCursor = 1
	model, write := update(t, model, key('s'))
	model, oldLoad := update(t, model, write())
	old := oldLoad()
	store.entries = append([]domain.Entry{{ID: 12, Title: "New"}}, store.entries...)
	model, refreshLoad := update(t, model, refreshMsg{})
	model, _ = update(t, model, primaryCommandMessage(t, refreshLoad))
	model, _ = update(t, model, old)
	if model.selectedEntryID() != 11 || len(model.entries) != 3 {
		t.Fatalf("refresh selection=%d entries=%#v", model.selectedEntryID(), model.entries)
	}
}

func TestInitialRefreshResumesAfterBusyOperationCompletes(t *testing.T) {
	for _, completion := range []tea.Msg{exportMsg{}, exportMsg{err: errors.New("export failed")}, addMsg{err: errors.New("add failed")}} {
		t.Run(fmt.Sprintf("%#v", completion), func(t *testing.T) {
			store := &fakeStore{feeds: []domain.Feed{{ID: 1, Title: "Feed"}}}
			refresher := &recordingRefresher{}
			model := New(store, refresher, nil)
			model.busy = true
			model, cmd := update(t, model, model.Init()())
			if cmd != nil || !model.initialRefreshPending {
				t.Fatal("initial refresh was not deferred while busy")
			}
			model, cmd = update(t, model, completion)
			if cmd == nil || model.initialRefreshPending || !model.busy {
				t.Fatal("busy completion did not resume startup refresh")
			}
			_ = primaryCommandMessage(t, cmd)
			if refresher.calls != 1 {
				t.Fatalf("refresh calls=%d", refresher.calls)
			}
			if msg, ok := completion.(exportMsg); ok && msg.err != nil && !model.errStatus {
				t.Fatal("startup refresh hid the export error")
			}
		})
	}
}
