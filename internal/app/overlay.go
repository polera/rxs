package app

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/polera/rxs/internal/ui"
)

func (m Model) updateOverlay(message tea.Msg) (tea.Model, tea.Cmd) {
	msg, isKeyPress := message.(tea.KeyPressMsg)
	if !isKeyPress {
		if isInputOverlay(m.overlay) {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(message)
			return m, cmd
		}
		return m, nil
	}

	key := msg.String()
	if m.overlay == colorSchemeOverlay {
		return m.updateColorSchemeChooser(key)
	}
	if m.overlay == quitOverlay {
		switch key {
		case "y", "enter":
			if m.active != readerPane || m.readerEntry == nil {
				return m, tea.Quit
			}
			store := m.store
			entryID := m.readerEntry.ID
			progress := m.reader.ScrollPercent()
			return m, func() tea.Msg {
				// Complete the local SQLite write before Bubble Tea exits.
				_ = store.SetReadingProgress(context.Background(), entryID, progress)
				return tea.Quit()
			}
		case "n", "q", "esc", "ctrl+c":
			m.closeOverlay()
		}
		return m, nil
	}
	if key == "esc" || key == "ctrl+c" || (key == "q" && (m.overlay == helpOverlay || m.overlay == deleteOverlay)) {
		m.closeOverlay()
		return m, nil
	}
	if m.overlay == helpOverlay {
		return m, nil
	}
	if m.overlay == deleteOverlay {
		if key == "y" || key == "enter" {
			feed := m.feeds[m.feedCursor-2]
			m.closeOverlay()
			m.busy = true
			return m, func() tea.Msg { return deleteMsg{err: m.store.DeleteFeed(context.Background(), feed.ID)} }
		}
		if key == "n" {
			m.closeOverlay()
		}
		return m, nil
	}
	if key == "enter" {
		value := strings.TrimSpace(m.input.Value())
		mode := m.overlay
		m.closeOverlay()
		if value == "" && mode == readerSearchOverlay {
			m.readerSearch = ""
			m.readerMatches = nil
			m.readerMatchCursor = -1
			m.renderReaderContent(m.currentReaderEntry())
			m.checkReaderReachedBottom()
			m.status, m.errStatus = "Article search cleared", false
			return m, nil
		}
		if value == "" && mode == searchOverlay {
			m.filter.Search = ""
			m.status, m.errStatus = "Search cleared", false
			return m, m.loadCmd()
		}
		if value == "" && mode == feedFilterOverlay {
			m.feedFilter = ""
			m.applyFeedSearch()
			m.resetFeedSelection()
			m.status, m.errStatus = "Feed filter cleared", false
			return m, m.loadCmd()
		}
		if value == "" {
			m.setError(fmt.Errorf("a value is required"))
			return m, nil
		}
		switch mode {
		case addOverlay:
			m.busy = true
			return m, func() tea.Msg {
				feed, err := m.store.AddFeed(context.Background(), value)
				return addMsg{feed: feed, err: err}
			}
		case searchOverlay:
			m.filter.Search = value
			m.entryCursor = 0
			m.status = "Search: " + value
			return m, m.loadCmd()
		case feedFilterOverlay:
			m.feedFilter = value
			m.applyFeedSearch()
			m.resetFeedSelection()
			m.status, m.errStatus = "Feed filter: "+value, false
			return m, m.loadCmd()
		case readerSearchOverlay:
			m.searchReader(value)
			return m, nil
		case importOverlay:
			m.busy = true
			return m, m.importCmd(value)
		case exportOverlay:
			m.busy = true
			return m, m.exportCmd(value)
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func isInputOverlay(mode overlay) bool {
	switch mode {
	case addOverlay, searchOverlay, feedFilterOverlay, readerSearchOverlay, importOverlay, exportOverlay:
		return true
	default:
		return false
	}
}

func (m Model) openColorSchemeChooser() (tea.Model, tea.Cmd) {
	m.overlay = colorSchemeOverlay
	m.schemeNames = ui.SchemeNames()
	m.schemeCursor = 0
	for index, name := range m.schemeNames {
		if name == m.styles.Name {
			m.schemeCursor = index
			break
		}
	}
	m.schemeOriginal = m.styles
	return m, nil
}

func (m Model) updateColorSchemeChooser(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c", "q":
		m.applyStyles(m.schemeOriginal)
		m.closeOverlay()
		return m, nil
	case "j", "down":
		m.previewColorScheme(1)
		return m, nil
	case "k", "up":
		m.previewColorScheme(-1)
		return m, nil
	case "enter":
		name := m.styles.Name
		m.closeOverlay()
		if m.saveScheme == nil {
			m.status, m.errStatus = "Color scheme: "+name, false
			return m, nil
		}
		save := m.saveScheme
		return m, func() tea.Msg {
			return colorSchemeSavedMsg{name: name, err: save(name)}
		}
	}
	return m, nil
}

func (m *Model) previewColorScheme(delta int) {
	if len(m.schemeNames) == 0 {
		return
	}
	m.schemeCursor = (m.schemeCursor + delta + len(m.schemeNames)) % len(m.schemeNames)
	styles, err := ui.ResolveScheme(m.schemeNames[m.schemeCursor])
	if err == nil {
		m.applyStyles(styles)
	}
}

func (m *Model) applyStyles(styles ui.Styles) {
	m.styles = styles
	inputStyles := m.input.Styles()
	inputStyles.Focused.Text = styles.Base
	inputStyles.Focused.Placeholder = styles.Dim
	inputStyles.Focused.Suggestion = styles.Dim
	inputStyles.Focused.Prompt = lipgloss.NewStyle().Foreground(styles.Scheme.Accent)
	inputStyles.Blurred.Text = styles.Dim
	inputStyles.Blurred.Placeholder = styles.Dim
	inputStyles.Blurred.Suggestion = styles.Dim
	inputStyles.Blurred.Prompt = styles.Dim
	inputStyles.Cursor.Color = styles.Scheme.Accent
	m.input.SetStyles(inputStyles)
	entry := m.currentReaderEntry()
	if entry.ID != 0 || entry.Title != "" || entry.Text != "" {
		m.renderReaderContent(entry)
	}
}

func (m *Model) openInput(mode overlay, prompt, placeholder string) (tea.Model, tea.Cmd) {
	m.overlay = mode
	m.input.Reset()
	switch mode {
	case searchOverlay:
		m.input.SetValue(m.filter.Search)
	case feedFilterOverlay:
		m.input.SetValue(m.feedFilter)
	case readerSearchOverlay:
		m.input.SetValue(m.readerSearch)
	}
	m.input.CursorEnd()
	m.input.Prompt = prompt + ": "
	m.input.Placeholder = placeholder
	m.input.SetWidth(max(20, min(70, m.width-10)))
	return *m, m.input.Focus()
}

func (m *Model) closeOverlay() {
	m.overlay = noOverlay
	m.input.Blur()
}
