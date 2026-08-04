package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/opml"
)

func (m Model) openBrowser() (tea.Model, tea.Cmd) {
	if len(m.entries) == 0 || m.active == feedsPane {
		return m, nil
	}
	url := m.entries[m.entryCursor].URL
	return m.openURL(url, "original article")
}

func (m Model) openURL(url, target string) (tea.Model, tea.Cmd) {
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

func (m Model) loadCmd() tea.Cmd {
	return m.loadCmdPreserving(0)
}

func (m Model) initialLoadCmd() tea.Cmd {
	return m.loadCmdWithOptions(0, true)
}

func (m Model) loadCmdPreserving(entryID int64) tea.Cmd {
	return m.loadCmdWithOptions(entryID, false)
}

func (m Model) loadCmdWithOptions(entryID int64, initial bool) tea.Cmd {
	filter := m.filter
	return func() tea.Msg {
		feeds, err := m.store.Feeds(context.Background())
		if err != nil {
			return loadedMsg{filter: filter, entryID: entryID, initial: initial, err: err}
		}
		entries, err := m.store.Entries(context.Background(), filter)
		return loadedMsg{
			feeds: feeds, entries: entries, filter: filter, entryID: entryID,
			initial: initial, err: err,
		}
	}
}

func (m Model) importCmd(path string) tea.Cmd {
	return func() tea.Msg {
		file, err := os.Open(filepath.Clean(path))
		if err != nil {
			return importMsg{err: fmt.Errorf("open OPML: %w", err)}
		}
		defer file.Close()
		subscriptions, err := opml.Import(file)
		if err != nil {
			return importMsg{err: err}
		}
		count := 0
		for _, subscription := range subscriptions {
			if _, err := m.store.AddFeed(context.Background(), subscription.FeedURL); err != nil {
				return importMsg{count: count, err: err}
			}
			count++
		}
		return importMsg{count: count}
	}
}

func (m Model) exportCmd(path string) tea.Cmd {
	feeds := append([]domain.Feed(nil), m.feeds...)
	return func() tea.Msg {
		clean := filepath.Clean(path)
		file, err := os.Create(clean)
		if err != nil {
			return exportMsg{err: fmt.Errorf("create OPML: %w", err)}
		}
		err = opml.Export(file, feeds)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		return exportMsg{path: clean, err: err}
	}
}

func (m *Model) setError(err error) {
	if err == nil {
		return
	}
	m.status, m.errStatus = err.Error(), true
}

func (m *Model) clampCursors() {
	m.feedCursor = clamp(m.feedCursor, 0, len(m.feeds)+1)
	m.entryCursor = clamp(m.entryCursor, 0, len(m.entries)-1)
}

func (m Model) selectedEntryID() int64 {
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
