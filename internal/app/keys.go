package app

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/polera/rxs/internal/domain"
)

func (m Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.pendingG {
		m.pendingG = false
		if key == "g" {
			if m.active == readerPane {
				m.reader.GotoTop()
				m.checkReaderReachedBottom()
				m.status, m.errStatus = "Beginning of article", false
				return m, nil
			}
			oldCursor, oldEntryID := m.entryCursor, m.selectedEntryID()
			if m.moveToListBoundary(false) {
				return m, m.loadCmd()
			}
			return m, m.markPreviewLeft(oldCursor, oldEntryID)
		}
	}
	if m.active == readerPane {
		switch key {
		case "/":
			return m.openInput(readerSearchOverlay, "Find in article", "search text")
		case "g":
			m.pendingG = true
			return m, nil
		case "G":
			m.reader.GotoBottom()
			m.checkReaderReachedBottom()
			m.status, m.errStatus = "End of article", false
			return m, nil
		case "ctrl+f":
			m.reader.PageDown()
			m.checkReaderReachedBottom()
			return m, nil
		case "ctrl+b":
			m.reader.PageUp()
			m.checkReaderReachedBottom()
			return m, nil
		case "ctrl+d":
			m.reader.HalfPageDown()
			m.checkReaderReachedBottom()
			return m, nil
		case "ctrl+u":
			m.reader.HalfPageUp()
			m.checkReaderReachedBottom()
			return m, nil
		case "n":
			m.selectReaderMatch(1)
			return m, nil
		case "N":
			m.selectReaderMatch(-1)
			return m, nil
		}
	}
	switch key {
	case "q", "ctrl+c":
		m.overlay = quitOverlay
		return m, nil
	case "?":
		m.overlay = helpOverlay
		return m, nil
	case "c":
		return m.openColorSchemeChooser()
	case "a":
		return m.openInput(addOverlay, "Feed URL", "https://example.com/feed.xml")
	case "/":
		if m.active == feedsPane {
			return m.openInput(feedFilterOverlay, "Filter feeds", "title or URL")
		}
		return m.openInput(searchOverlay, "Search", "title or article text")
	case "i":
		return m.openInput(importOverlay, "Import OPML", "path/to/subscriptions.opml")
	case "e":
		return m.openInput(exportOverlay, "Export OPML", "rxs-subscriptions.opml")
	case "h", "left":
		if m.active == readerPane {
			return m, m.leaveReader()
		}
		if m.active == articlesPane {
			m.active = feedsPane
			m.resizeReader()
		}
		return m, nil
	case "shift+tab":
		if m.active == readerPane && len(m.readerLinks) > 0 {
			m.selectReaderLink(-1)
			return m, nil
		}
		if m.active == readerPane {
			return m, m.leaveReader()
		}
		if m.active == articlesPane {
			m.active = feedsPane
			m.resizeReader()
		}
		return m, nil
	case "l", "right":
		if m.active == articlesPane {
			return m.enterReader()
		}
		if m.active == feedsPane {
			m.active = articlesPane
			m.resizeReader()
		}
		return m, nil
	case "tab":
		if m.active == readerPane && len(m.readerLinks) > 0 {
			m.selectReaderLink(1)
			return m, nil
		}
		if m.active == articlesPane {
			return m.enterReader()
		}
		if m.active == feedsPane {
			m.active = articlesPane
			m.resizeReader()
		}
		return m, nil
	case "esc":
		if m.active == readerPane && m.readerLinkCursor >= 0 {
			m.readerLinkCursor = -1
			m.renderReaderContent(m.currentReaderEntry())
		}
		return m, nil
	case "ctrl+f", "ctrl+b":
		if m.active != feedsPane {
			return m, nil
		}
		delta := m.listViewHeight()
		if key == "ctrl+b" {
			delta = -delta
		}
		oldFeed := m.feedCursor
		m.move(delta)
		if oldFeed != m.feedCursor {
			return m, m.loadCmd()
		}
		return m, nil
	case "g":
		m.pendingG = true
		return m, nil
	case "G":
		oldCursor, oldEntryID := m.entryCursor, m.selectedEntryID()
		if m.moveToListBoundary(true) {
			return m, m.loadCmd()
		}
		return m, m.markPreviewLeft(oldCursor, oldEntryID)
	case "j", "down":
		oldFeed := m.feedCursor
		oldCursor, oldEntryID := m.entryCursor, m.selectedEntryID()
		m.move(1)
		if m.active == feedsPane && oldFeed != m.feedCursor {
			return m, m.loadCmd()
		}
		return m, m.markPreviewLeft(oldCursor, oldEntryID)
	case "k", "up":
		oldFeed := m.feedCursor
		oldCursor, oldEntryID := m.entryCursor, m.selectedEntryID()
		m.move(-1)
		if m.active == feedsPane && oldFeed != m.feedCursor {
			return m, m.loadCmd()
		}
		return m, m.markPreviewLeft(oldCursor, oldEntryID)
	case "enter":
		return m.openSelected()
	case "space":
		return m.toggleRead()
	case "s":
		return m.toggleStarred()
	case "u":
		m.filter.UnreadOnly = !m.filter.UnreadOnly
		m.entryCursor = 0
		if m.filter.UnreadOnly {
			m.status = "Hiding read articles"
		} else {
			m.status = "Showing read articles"
		}
		return m, m.loadCmd()
	case "r":
		return m.refreshSelected()
	case "R":
		return m.refreshAll()
	case "d":
		if m.active == feedsPane && m.feedCursor >= 2 && len(m.feeds) > 0 {
			m.overlay = deleteOverlay
		}
		return m, nil
	case "o":
		return m.openBrowser()
	}
	if m.active == readerPane {
		var cmd tea.Cmd
		m.reader, cmd = m.reader.Update(msg)
		m.checkReaderReachedBottom()
		return m, cmd
	}
	return m, nil
}

func (m *Model) move(delta int) {
	switch m.active {
	case feedsPane:
		old := m.feedCursor
		m.feedCursor = clamp(m.feedCursor+delta, 0, len(m.feeds)+1)
		if old != m.feedCursor {
			m.entryCursor = 0
			m.applyFeedFilter()
			m.status, m.errStatus = "Loading articles…", false
		}
	case articlesPane:
		m.entryCursor = clamp(m.entryCursor+delta, 0, len(m.entries)-1)
		m.readerEntry = nil
		m.syncReader()
	case readerPane:
		if delta > 0 {
			m.reader.ScrollDown(1)
		} else {
			m.reader.ScrollUp(1)
		}
		m.checkReaderReachedBottom()
	}
}

// moveToListBoundary moves to the first or last row of the active list. It
// reports whether the feed selection changed and articles must be reloaded.
func (m *Model) moveToListBoundary(end bool) bool {
	switch m.active {
	case feedsPane:
		target := 0
		if end {
			target = len(m.feeds) + 1
		}
		if m.feedCursor == target {
			return false
		}
		m.feedCursor = target
		m.entryCursor = 0
		m.applyFeedFilter()
		m.status, m.errStatus = "Loading articles…", false
		return true
	case articlesPane:
		target := 0
		if end {
			target = max(0, len(m.entries)-1)
		}
		m.entryCursor = target
		m.readerEntry = nil
		m.syncReader()
	}
	return false
}

func (m *Model) applyFeedFilter() {
	m.filter.FeedID = 0
	m.filter.StarredOnly = false
	if m.feedCursor == 1 {
		m.filter.StarredOnly = true
	} else if m.feedCursor >= 2 && m.feedCursor-2 < len(m.feeds) {
		m.filter.FeedID = m.feeds[m.feedCursor-2].ID
	}
}

func (m *Model) applyFeedSearch() {
	term := strings.ToLower(strings.TrimSpace(m.feedFilter))
	if term == "" {
		m.feeds = append(m.feeds[:0], m.allFeeds...)
		return
	}
	m.feeds = m.feeds[:0]
	for _, feed := range m.allFeeds {
		if strings.Contains(strings.ToLower(feed.Title), term) ||
			strings.Contains(strings.ToLower(feed.URL), term) ||
			strings.Contains(strings.ToLower(feed.SiteURL), term) {
			m.feeds = append(m.feeds, feed)
		}
	}
}

func (m *Model) resetFeedSelection() {
	m.feedCursor, m.entryCursor = 0, 0
	m.filter.FeedID, m.filter.StarredOnly = 0, false
	m.readerEntry = nil
}

func (m Model) openSelected() (tea.Model, tea.Cmd) {
	if m.active == feedsPane {
		m.active = articlesPane
		return m, m.loadCmd()
	}
	if len(m.entries) == 0 {
		return m, nil
	}
	if m.active == readerPane && m.readerLinkCursor >= 0 && m.readerLinkCursor < len(m.readerLinks) {
		return m.openURL(m.readerLinks[m.readerLinkCursor].URL, "link")
	}
	return m.enterReader()
}

// enterReader starts a tracked reading session for the selected article.
// Reader focus is reachable through Enter as well as pane navigation, and all
// entry paths must initialize the snapshot and bottom latch used on exit.
func (m Model) enterReader() (tea.Model, tea.Cmd) {
	if len(m.entries) == 0 {
		m.active = readerPane
		m.readerEntry = nil
		m.readerReachedBottom = false
		m.resizeReader()
		return m, nil
	}
	entry := &m.entries[m.entryCursor]
	opened := *entry
	m.readerEntry = &opened
	m.readerReachedBottom = false
	m.readerSearch = ""
	m.readerMatches = nil
	m.readerMatchCursor = -1
	m.pendingG = false
	m.setReaderContent(opened)
	m.reader.GotoTop()
	m.active = readerPane
	m.resizeReader()
	m.checkReaderReachedBottom()
	return m, nil
}

func (m Model) toggleRead() (tea.Model, tea.Cmd) {
	if len(m.entries) == 0 || m.active == feedsPane {
		return m, nil
	}
	if m.active == readerPane && m.readerEntry != nil {
		return m, m.setRead(m.readerEntry.ID, !m.readerEntry.Read)
	}
	entry := m.entries[m.entryCursor]
	return m, m.setRead(entry.ID, !entry.Read)
}

func (m *Model) markPreviewLeft(oldCursor int, oldEntryID int64) tea.Cmd {
	if !m.markReadOnScroll || m.active != articlesPane ||
		oldCursor == m.entryCursor || oldEntryID == 0 {
		return nil
	}
	return m.setRead(oldEntryID, true)
}

func (m *Model) setRead(id int64, value bool) tea.Cmd {
	found, changed := false, false
	for index := range m.entries {
		if m.entries[index].ID != id {
			continue
		}
		found = true
		if m.entries[index].Read != value {
			changed = true
		}
	}
	if m.readerEntry != nil && m.readerEntry.ID == id {
		found = true
		if m.readerEntry.Read != value {
			changed = true
		}
	}
	if !found || !changed {
		return nil
	}
	for index := range m.entries {
		if m.entries[index].ID == id {
			m.entries[index].Read = value
		}
	}
	if m.readerEntry != nil && m.readerEntry.ID == id {
		m.readerEntry.Read = value
	}
	store := m.store
	return func() tea.Msg {
		return stateMsg{err: store.SetRead(context.Background(), id, value)}
	}
}

func (m *Model) leaveReader() tea.Cmd {
	if m.active != readerPane {
		return nil
	}
	reachedBottom := m.readerReachedBottom
	entry := m.readerEntry
	m.active = articlesPane
	m.resizeReader()
	if !reachedBottom || entry == nil {
		return nil
	}
	return m.setRead(entry.ID, true)
}

func (m Model) toggleStarred() (tea.Model, tea.Cmd) {
	if len(m.entries) == 0 || m.active == feedsPane {
		return m, nil
	}
	entry := &m.entries[m.entryCursor]
	entry.Starred = !entry.Starred
	if m.readerEntry != nil && m.readerEntry.ID == entry.ID {
		m.readerEntry.Starred = entry.Starred
	}
	value, id := entry.Starred, entry.ID
	return m, func() tea.Msg { return stateMsg{err: m.store.SetStarred(context.Background(), id, value)} }
}

func (m Model) refreshSelected() (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	if m.feedCursor < 2 {
		return m.refreshAll()
	}
	feed := m.feeds[m.feedCursor-2]
	m.busy = true
	m.status = "Refreshing " + feed.Title + "…"
	return m, m.refreshOneCmd(feed.ID)
}

func (m Model) refreshAll() (tea.Model, tea.Cmd) {
	if m.busy || len(m.allFeeds) == 0 {
		return m, nil
	}
	m.busy = true
	m.status = fmt.Sprintf("Refreshing %d feed(s)…", len(m.allFeeds))
	feeds := append([]domain.Feed(nil), m.allFeeds...)
	return m, func() tea.Msg {
		return refreshMsg{results: m.refresher.RefreshAll(context.Background(), feeds, 4)}
	}
}

func (m Model) refreshOneCmd(id int64) tea.Cmd {
	return func() tea.Msg {
		return refreshMsg{results: []domain.RefreshResult{m.refresher.Refresh(context.Background(), id)}}
	}
}
