package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/polera/rxs/internal/domain"
	feedservice "github.com/polera/rxs/internal/feed"
)

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for lifecycle event")
		var zero T
		return zero
	}
}

// Cancellation is observed inside the actual operation. It cannot finish until
// released, so a wrapper that merely returns early on ctx.Done fails these tests.
type cancelGate struct {
	started  chan context.Context
	canceled chan struct{}
	release  chan struct{}
}

func newCancelGate(t *testing.T) *cancelGate {
	t.Helper()
	g := &cancelGate{make(chan context.Context, 1), make(chan struct{}), make(chan struct{})}
	t.Cleanup(func() { close(g.release) })
	return g
}

func (g *cancelGate) wait(ctx context.Context) error {
	g.started <- ctx
	<-ctx.Done()
	close(g.canceled)
	<-g.release
	return ctx.Err()
}

type cancelRefresher struct{ gate *cancelGate }

func (r cancelRefresher) Refresh(ctx context.Context, id int64) domain.RefreshResult {
	return domain.RefreshResult{FeedID: id, Err: r.gate.wait(ctx)}
}

func (r cancelRefresher) RefreshAll(ctx context.Context, feeds []domain.Feed, _ int) []domain.RefreshResult {
	return []domain.RefreshResult{r.Refresh(ctx, feeds[0].ID)}
}

func TestConfirmedQuitCancelsRefreshButDrainsState(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "one", true: "all"}[all], func(t *testing.T) {
			model, store := loadedModel(t)
			original := model
			gate := newCancelGate(t)
			model.refresher = cancelRefresher{gate}
			var refresh tea.Cmd
			if all {
				next, cmd := model.refreshAll()
				model, refresh = next.(Model), cmd
			} else {
				model.busy = true
				refresh = model.refreshOneCmd(1)
			}
			refreshDone := make(chan tea.Msg, 1)
			go func() { refreshDone <- refresh() }()
			receive(t, gate.started)
			model.active = articlesPane
			model, first := update(t, model, key('s'))
			model, _ = update(t, model, key('l'))
			model, _ = update(t, model, key('q'))
			model, cmd := update(t, model, key('y'))
			if cmd != nil || !model.quitting || len(model.stateWrites) != 2 {
				t.Fatal("quit did not retain the star/progress queue")
			}
			receive(t, gate.canceled)
			model, progress := update(t, model, first())
			model, quit := update(t, model, progress())
			if _, ok := quit().(tea.QuitMsg); !ok || len(store.starCalls) != 1 || len(store.progressCalls) != 1 {
				t.Fatal("quit did not persist state independently of canceled refresh")
			}
			shutdownDone := make(chan error, 1)
			go func() { shutdownDone <- original.Shutdown(context.Background()) }()
			select {
			case err := <-shutdownDone:
				t.Fatalf("shutdown returned before refresh exited: %v", err)
			default:
			}
			gate.release <- struct{}{}
			receive(t, refreshDone)
			if err := receive(t, shutdownDone); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type cancelLoadStore struct {
	*fakeStore
	gate    *cancelGate
	entries bool
	first   bool
}

func (s *cancelLoadStore) Feeds(ctx context.Context) ([]domain.Feed, error) {
	if !s.entries && !s.first {
		s.first = true
		return nil, s.gate.wait(ctx)
	}
	return s.fakeStore.Feeds(ctx)
}

func (s *cancelLoadStore) Entries(ctx context.Context, filter domain.EntryFilter) ([]domain.Entry, error) {
	if s.entries && !s.first {
		s.first = true
		return nil, s.gate.wait(ctx)
	}
	return s.fakeStore.Entries(ctx, filter)
}

func TestSupersededLoadsAreCanceled(t *testing.T) {
	for _, entries := range []bool{false, true} {
		t.Run(map[bool]string{false: "feeds", true: "entries"}[entries], func(t *testing.T) {
			model, store := loadedModel(t)
			gate := newCancelGate(t)
			model.store = &cancelLoadStore{fakeStore: store, gate: gate, entries: entries}
			old := model.loadCmd()
			done := make(chan tea.Msg, 1)
			go func() { done <- old() }()
			receive(t, gate.started)
			current := model.loadCmd()
			receive(t, gate.canceled)
			gate.release <- struct{}{}
			stale := receive(t, done)
			if !errors.Is(stale.(loadedMsg).err, context.Canceled) {
				t.Fatal("superseded query was not canceled")
			}
			model, _ = update(t, model, current())
			model, _ = update(t, model, stale)
			if model.errStatus || len(model.entries) != 2 {
				t.Fatal("obsolete cancellation changed current view")
			}
			if err := model.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeferredStartupLoadCancellationRetainsRefreshIntent(t *testing.T) {
	model, _ := loadedModel(t)
	refresher := &recordingRefresher{}
	model.refresher = refresher
	startup := model.Init()
	current := model.loadCmd()
	model, _ = update(t, model, current())
	msg := startup().(loadedMsg)
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatal("deferred startup was not canceled")
	}
	model, cmd := update(t, model, msg)
	primaryCommandMessage(t, cmd)
	if refresher.calls != 1 {
		t.Fatalf("startup refresh calls = %d", refresher.calls)
	}
	if err := model.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUnexpectedShutdownCancelsAndJoinsLoad(t *testing.T) {
	model, store := loadedModel(t)
	gate := newCancelGate(t)
	model.store = &cancelLoadStore{fakeStore: store, gate: gate}
	load := model.loadCmd()
	done := make(chan tea.Msg, 1)
	go func() { done <- load() }()
	receive(t, gate.started)
	closed := make(chan error, 1)
	go func() {
		// This is the CLI's repository-close boundary.
		closed <- model.Shutdown(context.Background())
	}()
	receive(t, gate.canceled)
	select {
	case err := <-closed:
		t.Fatalf("store could close while query is still active: %v", err)
	default:
	}
	gate.release <- struct{}{}
	receive(t, done)
	if err := receive(t, closed); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownRejectsEveryDeferredStoreCommand(t *testing.T) {
	model, store := loadedModel(t)
	refresher := &recordingRefresher{}
	model.refresher = refresher
	commands := []tea.Cmd{model.Init(), model.refreshOneCmd(1), model.importCmd("missing"), model.exportCmd("missing")}
	_, refreshAll := model.refreshAll()
	commands = append(commands, refreshAll)
	model.overlay = addOverlay
	model.input.SetValue("https://example.test/new")
	_, add := model.updateOverlay(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	commands = append(commands, add)
	model.overlay = deleteOverlay
	model.deleteTarget = model.allFeeds[0]
	_, remove := model.updateOverlay(key('y'))
	commands = append(commands, remove)
	model.overlay = noOverlay
	model.active = articlesPane
	model, state := update(t, model, key('s'))
	commands = append(commands, state)
	if err := model.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The queued state intent was flushed once, without executing its Tea command.
	if !reflect.DeepEqual(store.starCalls, []bool{true}) {
		t.Fatalf("final flush = %v", store.starCalls)
	}
	for index, cmd := range commands {
		if cmd == nil || cmd() != nil {
			t.Fatalf("deferred command %d was not rejected", index)
		}
	}
	if len(store.addURLs) != 0 || len(store.deleteIDs) != 0 || len(store.starCalls) != 1 || refresher.calls != 0 {
		t.Fatal("a deferred command touched dependencies after shutdown")
	}
	if msg := model.Init()(); msg != nil {
		t.Fatal("command scheduled after shutdown was admitted")
	}
}

func TestUnexpectedShutdownFlushesQueuedIntentsFromOriginalModel(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "deferred", true: "undelivered completion"}[completed], func(t *testing.T) {
			model, store := loadedModel(t)
			original := model
			model.active = articlesPane
			model, first := update(t, model, key('s'))
			model, _ = update(t, model, key('s'))
			entry := model.entries[0]
			after := stateOf(entry)
			after.progress = 0.75
			model.queueStateWrite(entry, after, progressField)
			if completed {
				_ = first() // Tea exits without applying this completion.
			}
			if err := original.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(store.starCalls, []bool{true, false}) || !reflect.DeepEqual(store.progressCalls, []float64{0.75}) {
				t.Fatalf("final flush star=%v progress=%v", store.starCalls, store.progressCalls)
			}
			if err := model.Shutdown(context.Background()); err != nil || len(store.starCalls) != 2 {
				t.Fatal("repeated shutdown repeated writes")
			}
		})
	}
}

func TestUnexpectedShutdownReportsUndeliveredWriteFailure(t *testing.T) {
	model, store := loadedModel(t)
	store.starErr = errors.New("disk full")
	model.active = articlesPane
	model, cmd := update(t, model, key('s'))
	_ = cmd()
	if err := model.Shutdown(context.Background()); !errors.Is(err, store.starErr) {
		t.Fatalf("undelivered write error = %v", err)
	}
	if len(store.starCalls) != 1 {
		t.Fatal("failed write was silently replayed")
	}
}

type cancelStateStore struct {
	*fakeStore
	gate *cancelGate
}

func (s cancelStateStore) SetStarred(ctx context.Context, _ int64, _ bool) error {
	return s.gate.wait(ctx)
}

func TestFinalFlushCancellationJoinsActiveWriteAndReportsUnflushedIntent(t *testing.T) {
	model, store := loadedModel(t)
	gate := newCancelGate(t)
	model.store = cancelStateStore{store, gate}
	model.active = articlesPane
	model, cmd := update(t, model, key('s'))
	model, _ = update(t, model, key(' '))
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	writeCtx := receive(t, gate.started)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- model.Shutdown(ctx) }()
	cancel()
	receive(t, gate.canceled)
	if writeCtx.Err() == nil {
		t.Fatal("final flush did not cancel active state work")
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown abandoned active write: %v", err)
	default:
	}
	gate.release <- struct{}{}
	receive(t, done)
	if err := receive(t, shutdownDone); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "flush queued state") {
		t.Fatalf("shutdown error = %v", err)
	}
	if len(store.readCalls) != 0 {
		t.Fatal("queued intent started after final flush cancellation")
	}
}

func TestFinalFlushCancelsDeferredWrite(t *testing.T) {
	model, store := loadedModel(t)
	gate := newCancelGate(t)
	model.store = cancelStateStore{store, gate}
	model.active = articlesPane
	model, _ = update(t, model, key('s'))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- model.Shutdown(ctx) }()
	receive(t, gate.started) // Shutdown, not Tea, executes the deferred intent.
	cancel()
	receive(t, gate.canceled)
	select {
	case err := <-done:
		t.Fatalf("flush returned before write exited: %v", err)
	default:
	}
	gate.release <- struct{}{}
	if err := receive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("flush error = %v", err)
	}
}

func TestFailedQuitRestoresUsableBackgroundLifetime(t *testing.T) {
	model, store := loadedModel(t)
	gate := newCancelGate(t)
	model.refresher = cancelRefresher{gate}
	model.busy = true
	refresh := model.refreshOneCmd(1)
	done := make(chan tea.Msg, 1)
	go func() { done <- refresh() }()
	receive(t, gate.started)
	model.active = articlesPane
	model, _ = update(t, model, key('l'))
	store.progressErr = errors.New("progress failed")
	model, _ = update(t, model, key('q'))
	model, save := update(t, model, key('y'))
	receive(t, gate.canceled)
	model, load := update(t, model, save())
	msg := primaryCommandMessage(t, load).(loadedMsg)
	if msg.err != nil || model.quitting {
		t.Fatal("failed quit left the application context canceled")
	}
	model, _ = update(t, model, msg)
	gate.release <- struct{}{}
	model, _ = update(t, model, receive(t, done))
	if model.busy || !strings.Contains(model.status, "progress failed") {
		t.Fatal("canceled refresh hid failed write or retained busy state")
	}
	model.refresher = fakeRefresher{}
	if result := model.refreshOneCmd(1)().(refreshMsg); result.canceled {
		t.Fatal("new refresh inherited canceled background context")
	}
	store.progressErr = nil
	model, _ = update(t, model, key('q'))
	model, save = update(t, model, key('y'))
	_, quit := update(t, model, save())
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("retry quit did not succeed")
	}
	if err := model.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type partialImportStore struct{ *fakeStore }

func (s partialImportStore) AddFeed(ctx context.Context, url string) (domain.Feed, error) {
	if len(s.addURLs) == 1 {
		return domain.Feed{}, errors.New("disk full")
	}
	return s.fakeStore.AddFeed(ctx, url)
}

func TestPartialImportShowsSuccessfulCount(t *testing.T) {
	model, store := loadedModel(t)
	model.store = partialImportStore{store}
	path := filepath.Join(t.TempDir(), "feeds.opml")
	if err := os.WriteFile(path, []byte(`<opml version="2.0"><body><outline xmlUrl="https://one.test/rss"/><outline xmlUrl="https://two.test/rss"/></body></opml>`), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := model.importCmd(path)().(importMsg)
	if msg.count != 1 || msg.err == nil {
		t.Fatalf("partial import = %#v", msg)
	}
	model, load := update(t, model, msg)
	if !model.errStatus || model.status != "Imported 1 subscription(s) before import failed: disk full" || load == nil {
		t.Fatalf("partial import status = %q", model.status)
	}
	if err := model.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshAllClonesSubscriptionSnapshot(t *testing.T) {
	model, _ := loadedModel(t)
	refresher := &recordingRefresher{}
	model.refresher = refresher
	_, cmd := model.refreshAll()
	model.allFeeds[0].Title = "changed"
	cmd()
	if refresher.feeds[0].Title != "Feed" {
		t.Fatal("refresh command retained mutable display slice")
	}
	if err := model.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type canceledFetch struct{ gate *cancelGate }

func (f canceledFetch) Fetch(ctx context.Context, _ domain.Feed) (domain.ParsedFeed, error) {
	_ = f.gate.wait(ctx)
	// A dependency can finish successfully at the cancellation boundary. The
	// service must still avoid persisting or recording a cancellation as failure.
	return domain.ParsedFeed{}, nil
}

type canceledRefreshRepository struct {
	t      *testing.T
	loaded []int64
}

func (r *canceledRefreshRepository) Feed(_ context.Context, id int64) (domain.Feed, error) {
	r.loaded = append(r.loaded, id)
	return domain.Feed{ID: id}, nil
}

func (r *canceledRefreshRepository) ApplyRefresh(context.Context, int64, domain.ParsedFeed) (int, error) {
	r.t.Error("canceled fetch was persisted")
	return 0, nil
}

func (r *canceledRefreshRepository) RecordRefreshError(context.Context, int64, error) error {
	r.t.Error("cancellation was recorded as a refresh failure")
	return nil
}

func TestFeedServiceCancellationJoinsWorkersAndStopsQueuedFeeds(t *testing.T) {
	gate := newCancelGate(t)
	repository := &canceledRefreshRepository{t: t}
	service := feedservice.NewService(repository, canceledFetch{gate})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan []domain.RefreshResult, 1)
	go func() {
		done <- service.RefreshAll(ctx, []domain.Feed{{ID: 1}, {ID: 2}, {ID: 3}}, 1)
	}()
	receive(t, gate.started)
	cancel()
	receive(t, gate.canceled)
	select {
	case <-done:
		t.Fatal("RefreshAll returned before its worker exited")
	default:
	}
	gate.release <- struct{}{}
	results := receive(t, done)
	if len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) || !reflect.DeepEqual(repository.loaded, []int64{1}) {
		t.Fatalf("canceled pool results=%#v loaded=%v", results, repository.loaded)
	}
}

type committedDeleteStore struct{ *fakeStore }

func (s committedDeleteStore) DeleteFeed(_ context.Context, id int64) error {
	s.feeds = slices.DeleteFunc(s.feeds, func(feed domain.Feed) bool { return feed.ID == id })
	s.entries = slices.DeleteFunc(s.entries, func(entry domain.Entry) bool { return entry.FeedID == id })
	return nil
}

func TestCommittedDeletionDuringQuitSurvivesStateFailure(t *testing.T) {
	model, store := loadedModel(t)
	store.feeds = append(store.feeds, domain.Feed{ID: 2, Title: "Other", URL: "https://other.test/rss"})
	store.entries = append(store.entries, domain.Entry{ID: 20, FeedID: 2, Title: "Other article"})
	model, _ = update(t, model, loadedMsg{feeds: slices.Clone(store.feeds), entries: slices.Clone(store.entries)})
	model.store = committedDeleteStore{store}
	store.starErr = errors.New("state write failed")
	model.active = articlesPane
	model.filter.FeedID = 1
	model, write := update(t, model, key('s'))
	model.active = feedsPane
	model.feedCursor = 2
	model, _ = update(t, model, key('d'))
	model, remove := update(t, model, key('y'))
	committed := remove().(deleteMsg) // Hold the committed result until quit.
	if committed.feedID != 1 || committed.err != nil {
		t.Fatalf("deletion completion = %#v", committed)
	}
	model, _ = update(t, model, key('q'))
	model, cmd := update(t, model, key('y'))
	if cmd != nil || !model.quitting {
		t.Fatal("quit did not wait for state write")
	}
	status := model.status
	model, cmd = update(t, model, committed)
	if cmd != nil || !model.quitting || model.status != status || model.deleting || model.busy {
		t.Fatal("deletion completion presented status or scheduled work during quit")
	}
	if model.filter.FeedID != 0 || model.feedCursor != 0 || len(model.feeds) != 1 || model.feeds[0].ID != 2 || len(model.entries) != 1 || model.entries[0].FeedID != 2 {
		t.Fatal("committed deletion was not reconciled while quitting")
	}
	model, load := update(t, model, write())
	msg := primaryCommandMessage(t, load).(loadedMsg)
	if msg.filter.FeedID != 0 || model.quitting || model.status != "state write failed" {
		t.Fatal("failed quit resumed the deleted feed filter")
	}
	model, _ = update(t, model, msg)
	if model.active != feedsPane || model.readerEntry != nil || model.selectedEntryID() != 20 {
		t.Fatal("resumed application retained deleted article identity")
	}
	if err := model.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type cancelImportStore struct {
	*fakeStore
	gate       *cancelGate
	successful int
}

func (s cancelImportStore) AddFeed(ctx context.Context, url string) (domain.Feed, error) {
	if len(s.addURLs) < s.successful {
		return s.fakeStore.AddFeed(ctx, url)
	}
	return domain.Feed{}, s.gate.wait(ctx)
}

func TestCanceledImportExportPreserveStateFailureInEitherOrder(t *testing.T) {
	for _, operation := range []string{"import", "partial import", "export"} {
		for _, backgroundFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/backgroundFirst=%t", operation, backgroundFirst), func(t *testing.T) {
				model, store := loadedModel(t)
				gate := newCancelGate(t)
				var background tea.Cmd
				count := 0
				if operation == "export" {
					model.store = &cancelLoadStore{fakeStore: store, gate: gate}
					background = model.exportCmd(filepath.Join(t.TempDir(), "feeds.opml"))
				} else {
					if operation == "partial import" {
						count = 1
					}
					model.store = cancelImportStore{fakeStore: store, gate: gate, successful: count}
					path := filepath.Join(t.TempDir(), "feeds.opml")
					if err := os.WriteFile(path, []byte(importFixture), 0o600); err != nil {
						t.Fatal(err)
					}
					background = model.importCmd(path)
				}
				model.busy = true
				done := make(chan tea.Msg, 1)
				go func() { done <- background() }()
				receive(t, gate.started)
				store.starErr = errors.New("state write failed")
				model.active = articlesPane
				model, write := update(t, model, key('s'))
				model, _ = update(t, model, key('q'))
				model, _ = update(t, model, key('y'))
				receive(t, gate.canceled)
				if !backgroundFirst {
					model, _ = update(t, model, write())
				}
				gate.release <- struct{}{}
				model, _ = update(t, model, receive(t, done))
				if backgroundFirst {
					model, _ = update(t, model, write())
				}
				if model.quitting || model.busy || !model.errStatus || model.status != "state write failed" {
					t.Fatalf("cancellation hid failed state write: quitting=%t busy=%t status=%q", model.quitting, model.busy, model.status)
				}
				if model.canceledImportCount != count {
					t.Fatalf("partial count = %d, want %d", model.canceledImportCount, count)
				}
				if count > 0 && !strings.Contains(model.View().Content, "Import canceled after 1 subscription(s)") {
					t.Fatal("partial import success was not presented separately")
				}
				model, _ = update(t, model, model.loadCmdPreserving()())
				if model.status != "state write failed" {
					t.Fatal("reload hid failed state write")
				}
				model.clearStatus()
				if model.canceledImportCount != 0 {
					t.Fatal("cleared status retained obsolete import annotation")
				}
				if err := model.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
