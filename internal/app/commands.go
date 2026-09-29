package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/opml"
	"github.com/polera/rxs/internal/safefile"
)

type pagedStore interface {
	EntriesPage(context.Context, domain.EntryFilter, domain.EntryCursor, bool, int) ([]domain.Entry, error)
	Entry(context.Context, int64) (domain.Entry, error)
}

type anchoredStore interface {
	EntryPageAt(context.Context, domain.EntryFilter, int64, int) ([]domain.Entry, bool, bool, bool, error)
}

type lastViewStore interface {
	LoadLastView(context.Context) (domain.LastView, bool, error)
	SaveLastView(context.Context, domain.LastView) error
}

func cursorFor(entry domain.Entry) domain.EntryCursor {
	return domain.EntryCursor{Date: entry.SortDate, ID: entry.ID}
}

func (m *Model) resetPage() {
	m.page = pageRequest{}
	m.pagePrior = pageRequest{}
	m.hasPrevious, m.hasNext = false, false
	m.pageLoading = false
	m.pageLeaving = domain.Entry{}
	m.entryCursor = 0
}

func (m *Model) pageTo(reverse bool, cursor domain.EntryCursor) tea.Cmd {
	if m.active == articlesPane && m.entryCursor >= 0 && m.entryCursor < len(m.entries) {
		m.pageLeaving = m.entries[m.entryCursor]
	}
	m.pagePrior = m.page
	m.page = pageRequest{cursor: cursor, reverse: reverse}
	m.pageLoading = true
	m.setPersistentStatus("Loading articles…", false)
	m.loadGeneration++
	return m.loadCmdWithOptions(false, false)
}

func (m *Model) loadBodyCmd() tea.Cmd {
	store, ok := m.store.(pagedStore)
	if !ok {
		return nil
	}
	// Narrow layouts don't show a preview until the reader is opened.
	if m.active != readerPane && m.width < 110 {
		return nil
	}
	var entry domain.Entry
	if m.active == readerPane && m.readerEntry != nil {
		entry = *m.readerEntry
	} else if m.entryCursor >= 0 && m.entryCursor < len(m.entries) {
		entry = m.entries[m.entryCursor]
	} else {
		return nil
	}
	if !entry.Unloaded {
		return nil
	}
	id, generation := entry.ID, m.bodyGeneration
	return m.lifetime.command(m.lifetime.bodyContext(), func(ctx context.Context) tea.Msg {
		if err := ctx.Err(); err != nil {
			return bodyMsg{id: id, generation: generation, err: err}
		}
		body, err := store.Entry(ctx, id)
		return bodyMsg{id: id, generation: generation, entry: body, err: err}
	})
}

func (m *Model) openBrowser() (tea.Model, tea.Cmd) {
	entry, ok := m.articleActionTarget()
	if !ok {
		return m, nil
	}
	return m.openURL(entry.URL, "original article")
}

func (m *Model) copyArticleURL() (tea.Model, tea.Cmd) {
	entry, ok := m.articleActionTarget()
	if !ok {
		return m, nil
	}
	url := entry.URL
	if strings.TrimSpace(url) == "" {
		m.setError(fmt.Errorf("article has no URL"))
		return m, nil
	}
	m.setStatus("Copied article URL", false)
	return m, tea.SetClipboard(url)
}

func (m *Model) openURL(url, target string) (tea.Model, tea.Cmd) {
	if m.tuiBrowser != nil {
		command, err := m.tuiBrowser(url)
		if err != nil {
			return m, func() tea.Msg { return browserMsg{target: target, err: err} }
		}
		if command == nil {
			return m, func() tea.Msg {
				return browserMsg{target: target, err: fmt.Errorf("TUI browser returned no command")}
			}
		}
		return m, tea.ExecProcess(command, func(err error) tea.Msg {
			return browserMsg{target: target, err: err}
		})
	}
	if m.browser == nil {
		return m, func() tea.Msg { return browserMsg{target: target, err: fmt.Errorf("no browser is configured")} }
	}
	return m, func() tea.Msg { return browserMsg{target: target, err: m.browser(url)} }
}

func (m *Model) articleActionTarget() (domain.Entry, bool) {
	if m.active == readerPane && m.readerEntry != nil {
		return *m.readerEntry, true
	}
	if m.active == articlesPane && m.entryCursor >= 0 && m.entryCursor < len(m.entries) {
		return m.entries[m.entryCursor], true
	}
	return domain.Entry{}, false
}

func (m *Model) loadCmd() tea.Cmd {
	m.loadGeneration++
	m.loadAnchorID = 0
	return m.loadCmdWithOptions(false, false)
}

func (m *Model) loadCmdPreserving() tea.Cmd {
	m.loadGeneration++
	m.loadAnchorID = m.selectedEntryID()
	if m.active == readerPane && m.readerEntry != nil {
		m.loadAnchorID = m.readerEntry.ID
	}
	return m.loadCmdWithOptions(true, false)
}

func (m *Model) loadCmdWithOptions(preserveSelection, initial bool) tea.Cmd {
	filter, generation, page := m.filter, m.loadGeneration, m.page
	anchorID := m.loadAnchorID
	m.loadAnchorID = 0
	feedsSnapshot := slices.Clone(m.allFeeds)
	readLastView := initial && m.resumeLastView
	loadFeeds := !m.pageLoading || !m.hasLoaded
	allowShowRead := m.resuming
	return m.lifetime.command(m.lifetime.backgroundContext(true), func(ctx context.Context) tea.Msg {
		if err := ctx.Err(); err != nil {
			return loadedMsg{filter: filter, generation: generation, initial: initial, err: err}
		}
		feeds := feedsSnapshot
		var err error
		var view domain.LastView
		var viewFound bool
		var viewErr error
		if readLastView {
			if store, ok := m.store.(lastViewStore); ok {
				view, viewFound, viewErr = store.LoadLastView(ctx)
			}
		}
		if loadFeeds {
			feeds, err = m.store.Feeds(ctx)
			if err != nil {
				return loadedMsg{filter: filter, page: page, generation: generation, initial: initial, err: err}
			}
		}
		if err := ctx.Err(); err != nil {
			return loadedMsg{filter: filter, generation: generation, initial: initial, err: err}
		}
		var entries []domain.Entry
		hasPrevious, hasNext := false, false
		anchorFound, showRead := false, false
		if store, ok := m.store.(pagedStore); ok {
			if anchorID != 0 && page == (pageRequest{}) {
				if anchored, ok := m.store.(anchoredStore); ok {
					entries, hasPrevious, hasNext, anchorFound, err = anchored.EntryPageAt(ctx, filter, anchorID, entryPageSize)
					if err == nil && !anchorFound && filter.UnreadOnly && allowShowRead {
						visible := filter
						visible.UnreadOnly = false
						entries, hasPrevious, hasNext, anchorFound, err = anchored.EntryPageAt(ctx, visible, anchorID, entryPageSize)
						showRead = anchorFound
					}
				}
			}
			if err == nil && !anchorFound {
				entries, err = store.EntriesPage(ctx, filter, page.cursor, page.reverse, entryPageSize+1)
			} else if err == nil {
				return loadedMsg{feeds: feeds, entries: entries, filter: filter, page: page,
					hasPrevious: hasPrevious, hasNext: hasNext, generation: generation, anchorID: anchorID,
					anchorFound: true, showRead: showRead, preserveSelection: preserveSelection, initial: initial,
					lastView: view, lastViewFound: viewFound, lastViewErr: viewErr}
			}
			if err == nil {
				more := len(entries) > entryPageSize
				if more {
					entries = entries[:entryPageSize]
				}
				if page.reverse {
					slices.Reverse(entries)
					hasPrevious, hasNext = more, page.cursor.ID != 0
				} else {
					hasPrevious, hasNext = page.cursor.ID != 0, more
				}
			}
		} else {
			entries, err = m.store.Entries(ctx, filter)
		}
		return loadedMsg{
			feeds: feeds, entries: entries, filter: filter, page: page, hasPrevious: hasPrevious, hasNext: hasNext, generation: generation,
			preserveSelection: preserveSelection, initial: initial, err: err,
			lastView: view, lastViewFound: viewFound, lastViewErr: viewErr, anchorID: anchorID,
		}
	})
}

func (m *Model) startRestore(view domain.LastView) tea.Cmd {
	if view.Pane != "feeds" && view.Pane != "articles" && view.Pane != "reader" {
		return nil
	}
	m.feedCursor = 0
	switch view.Scope {
	case "all":
	case "starred":
		m.feedCursor = 1
	case "feed":
		for i, feed := range m.feeds {
			if feed.ID == view.FeedID {
				m.feedCursor = i + 2
				break
			}
		}
	default:
		return nil
	}
	m.active = feedsPane
	m.resumePane = feedsPane
	if view.Pane != "feeds" {
		m.active = articlesPane
		m.resumePane = m.active
		if view.Pane == "reader" {
			m.resumePane = readerPane
		}
	}
	m.feedFilter = ""
	m.filter.Search = ""
	m.resetPage()
	m.applyFeedFilter()
	m.readerEntry = nil
	m.resuming = true
	m.loadAnchorID = view.EntryID
	m.resizeReader()
	m.loadGeneration++
	return m.loadCmdWithOptions(false, false)
}

func (m *Model) finishInitialRefresh() (tea.Model, tea.Cmd) {
	if !m.initialRefreshPending || !m.hasLoaded || m.busy || m.quitting {
		return m, nil
	}
	m.initialRefreshPending = false
	status, hadError := m.status, m.errStatus
	next, cmd := m.refreshAll()
	if hadError {
		updated := next.(*Model)
		updated.setStatus(status, true)
		return updated, cmd
	}
	return next, cmd
}

func (m *Model) importCmd(path string) tea.Cmd {
	return m.lifetime.command(m.lifetime.backgroundContext(false), func(ctx context.Context) tea.Msg {
		if err := ctx.Err(); err != nil {
			return importMsg{err: err}
		}
		file, err := openImportFile(ctx, filepath.Clean(path))
		if err != nil {
			return importMsg{err: fmt.Errorf("open OPML: %w", err)}
		}
		defer file.Close()
		subscriptions, err := opml.Import(importReader{ctx: ctx, reader: file})
		if ctx.Err() != nil {
			return importMsg{err: ctx.Err()}
		}
		if err != nil {
			return importMsg{err: err}
		}
		count := 0
		for _, subscription := range subscriptions {
			if err := ctx.Err(); err != nil {
				return importMsg{count: count, err: err}
			}
			if _, err := m.store.AddFeed(ctx, subscription.FeedURL); err != nil {
				return importMsg{count: count, err: err}
			}
			count++
		}
		return importMsg{count: count}
	})
}

func (m *Model) exportCmd(path string) tea.Cmd {
	store := m.store
	return m.lifetime.command(m.lifetime.backgroundContext(false), func(ctx context.Context) tea.Msg {
		clean := filepath.Clean(path)
		if err := ctx.Err(); err != nil {
			return exportMsg{path: clean, err: err}
		}
		feeds, err := store.Feeds(ctx)
		if err != nil {
			return exportMsg{path: clean, err: fmt.Errorf("load subscriptions for export: %w", err)}
		}
		err = safefile.Write(clean, 0o600, func(writer io.Writer) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return opml.Export(writer, feeds)
		})
		return exportMsg{path: clean, err: err}
	})
}

func (m *Model) setError(err error) {
	if err == nil {
		return
	}
	m.setStatus(err.Error(), true)
}

func (m *Model) clampCursors() {
	m.feedCursor = clamp(m.feedCursor, 0, len(m.feeds)+1)
	m.entryCursor = clamp(m.entryCursor, 0, len(m.entries)-1)
}

func (m *Model) selectedEntryID() int64 {
	if m.entryCursor >= 0 && m.entryCursor < len(m.entries) {
		return m.entries[m.entryCursor].ID
	}
	return 0
}

func (m *Model) restoreEntrySelection(id int64) {
	for index := range m.entries {
		if m.entries[index].ID == id {
			m.entryCursor = index
			return
		}
	}
}

func (m *Model) reconcileFeedCursor() {
	if m.filter.StarredOnly {
		m.feedCursor = 1
		return
	}
	if m.filter.FeedID == 0 {
		m.feedCursor = 0
		return
	}
	for index, source := range m.feeds {
		if source.ID == m.filter.FeedID {
			m.feedCursor = index + 2
			return
		}
	}
}
