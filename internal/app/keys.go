package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/polera/rxs/internal/domain"
)

func (m Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if isNavigationKey(key, m.pendingG) {
		m.clearStatus()
	}
	if m.pendingG {
		m.pendingG = false
		if key == "g" {
			if m.active == readerPane {
				m.reader.GotoTop()
				m.checkReaderReachedBottom()
				m.setStatus("Beginning of article", false)
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
			m.setStatus("End of article", false)
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
	case "y":
		return m.copyArticleURL()
	case "u":
		m.filter.UnreadOnly = !m.filter.UnreadOnly
		m.entryCursor = 0
		if m.filter.UnreadOnly {
			m.setStatus("Hiding read articles", false)
		} else {
			m.setStatus("Showing read articles", false)
		}
		return m, m.loadCmd()
	case "r":
		return m.refreshSelected()
	case "R":
		return m.refreshAll()
	case "d":
		if !m.busy && !m.deleting && m.active == feedsPane && m.feedCursor >= 2 && m.feedCursor-2 < len(m.feeds) {
			m.deleteTarget = m.feeds[m.feedCursor-2]
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

func isNavigationKey(key string, pendingG bool) bool {
	switch key {
	case "j", "down", "k", "up", "h", "left", "l", "right",
		"tab", "shift+tab", "enter", "esc", "G", "ctrl+f", "ctrl+b",
		"ctrl+d", "ctrl+u", "n", "N":
		return true
	case "g":
		return pendingG
	default:
		return false
	}
}

func (m *Model) move(delta int) {
	switch m.active {
	case feedsPane:
		old := m.feedCursor
		m.feedCursor = clamp(m.feedCursor+delta, 0, len(m.feeds)+1)
		if old != m.feedCursor {
			m.entryCursor = 0
			m.applyFeedFilter()
			m.setPersistentStatus("Loading articles…", false)
		}
	case articlesPane:
		target := clamp(m.entryCursor+delta, 0, len(m.entries)-1)
		if target == m.entryCursor {
			return
		}
		m.entryCursor = target
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
		m.setPersistentStatus("Loading articles…", false)
		return true
	case articlesPane:
		target := 0
		if end {
			target = max(0, len(m.entries)-1)
		}
		if target == m.entryCursor {
			return false
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
	if m.active == readerPane {
		if m.readerLinkCursor >= 0 && m.readerLinkCursor < len(m.readerLinks) {
			return m.openURL(m.readerLinks[m.readerLinkCursor].URL, "link")
		}
		return m, nil
	}
	return m.enterReader()
}

// enterReader starts a tracked reading session for the selected article.
// Reader focus is reachable through Enter as well as pane navigation, and all
// entry paths must initialize the snapshot and bottom latch used on exit.
func (m Model) enterReader() (tea.Model, tea.Cmd) {
	if len(m.entries) == 0 {
		return m, nil
	}
	entry := &m.entries[m.entryCursor]
	opened := *entry
	m.readerEntry = &opened
	m.readerSearch = ""
	m.readerMatches = nil
	m.readerMatchCursor = -1
	m.readerLinkCursor = -1
	m.pendingG = false
	m.reader.GotoTop()
	m.active = readerPane
	m.resizeReader()
	// Geometry changes are not reading progress; latch only the restored view.
	m.readerReachedBottom = false
	m.restoreReaderProgress(opened.ReadingProgress)
	m.checkReaderReachedBottom()
	return m, nil
}

func (m Model) toggleRead() (tea.Model, tea.Cmd) {
	entry, ok := m.articleActionTarget()
	if !ok {
		return m, nil
	}
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
	var entry domain.Entry
	for _, candidate := range m.entries {
		if candidate.ID == id {
			entry = candidate
			break
		}
	}
	if m.readerEntry != nil && m.readerEntry.ID == id && (entry.ID == 0 || m.active == readerPane) {
		entry = *m.readerEntry
	}
	if entry.ID == 0 || entry.Read == value {
		return nil
	}
	after := stateOf(entry)
	after.read = value
	return m.queueStateWrite(entry, after, readField)
}

func (m *Model) leaveReader() tea.Cmd {
	if m.active != readerPane {
		return nil
	}
	entry := m.readerEntry
	var cmd tea.Cmd
	if entry != nil {
		after := stateOf(*entry)
		after.progress = m.reader.ScrollPercent()
		fields := progressField
		if m.readerReachedBottom && !entry.Read {
			after.read = true
			fields |= readField
		}
		cmd = m.queueStateWrite(*entry, after, fields)
	}
	m.active = articlesPane
	m.resizeReader()
	return cmd
}

func (m Model) toggleStarred() (tea.Model, tea.Cmd) {
	entry, ok := m.articleActionTarget()
	if !ok {
		return m, nil
	}
	after := stateOf(entry)
	after.starred = !entry.Starred
	return m, m.queueStateWrite(entry, after, starredField)
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
	m.setPersistentStatus("Refreshing "+feed.Title+"…", false)
	return m, m.refreshOneCmd(feed.ID)
}

func (m Model) refreshAll() (tea.Model, tea.Cmd) {
	if m.busy || len(m.allFeeds) == 0 {
		return m, nil
	}
	m.busy = true
	m.setPersistentStatus(fmt.Sprintf("Refreshing %d feed(s)…", len(m.allFeeds)), false)
	feeds := slices.Clone(m.allFeeds)
	return m, m.lifetime.command(m.lifetime.backgroundContext(false), func(ctx context.Context) tea.Msg {
		if err := ctx.Err(); err != nil {
			return refreshMsg{canceled: true}
		}
		results := m.refresher.RefreshAll(ctx, feeds, 4)
		return refreshMsg{results: results, canceled: ctx.Err() != nil}
	})
}

func (m Model) refreshOneCmd(id int64) tea.Cmd {
	return m.lifetime.command(m.lifetime.backgroundContext(false), func(ctx context.Context) tea.Msg {
		if err := ctx.Err(); err != nil {
			return refreshMsg{canceled: true}
		}
		result := m.refresher.Refresh(ctx, id)
		return refreshMsg{results: []domain.RefreshResult{result}, canceled: ctx.Err() != nil}
	})
}
