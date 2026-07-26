package app

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m Model) View() tea.View {
	if m.overlay != noOverlay {
		view := m.newView(m.overlayView())
		view.AltScreen = true
		return view
	}
	bodyHeight := max(4, m.height-2)
	var body string
	switch {
	case m.active == readerPane:
		body = m.styles.Pane("Reader", true, m.width, bodyHeight, m.reader.View())
	case m.width >= 110:
		fw := m.width / 4
		aw := m.width / 3
		rw := m.width - fw - aw
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			m.styles.Pane("Feeds", m.active == feedsPane, fw, bodyHeight, m.feedsView(fw-4)),
			m.styles.Pane("Articles", m.active == articlesPane, aw, bodyHeight, m.entriesView(aw-4)),
			m.styles.Pane("Reader", m.active == readerPane, rw, bodyHeight, m.reader.View()))
	case m.width >= 70:
		left := m.width / 2
		right := m.width - left
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			m.styles.Pane("Feeds", m.active == feedsPane, left, bodyHeight, m.feedsView(left-4)),
			m.styles.Pane("Articles", m.active == articlesPane, right, bodyHeight, m.entriesView(right-4)))
	default:
		switch m.active {
		case feedsPane:
			body = m.styles.Pane("Feeds", true, m.width, bodyHeight, m.feedsView(m.width-4))
		case articlesPane:
			body = m.styles.Pane("Articles", true, m.width, bodyHeight, m.entriesView(m.width-4))
		case readerPane:
			body = m.styles.Pane("Reader", true, m.width, bodyHeight, m.reader.View())
		}
	}
	statusStyle := m.styles.Dim
	if m.errStatus {
		statusStyle = m.styles.Danger
	} else if m.status != "" && m.status == m.warningStatus {
		statusStyle = m.styles.Warning
	}
	statusText := m.status
	if m.busy {
		statusText = "◌ " + statusText
	}
	status := statusStyle.Render(truncate(statusText, max(1, m.width-2)))
	keyText := "j/k move · ctrl+f/b page · gg/G start/end · h/l pane · enter read · r refresh · / filter · c colors · ? help"
	if m.active == articlesPane {
		keyText = "j/k move · gg/G start/end · h/l pane · enter read · u show/hide read · / search · ? help"
	} else if m.active == readerPane {
		keyText = "j/k scroll · ctrl+f/b page · gg/G start/end · / find · n/N matches · h articles · c colors · ? help"
	}
	keys := m.styles.Dim.Render(truncate(keyText, max(1, m.width-2)))
	view := m.newView(body + "\n" + status + "\n" + keys)
	view.AltScreen = true
	return view
}

func (m Model) newView(content string) tea.View {
	view := tea.NewView(content)
	view.ForegroundColor = m.styles.Scheme.Foreground
	view.BackgroundColor = m.styles.Scheme.Background
	return view
}

func (m Model) feedsView(width int) string {
	var lines []string
	selectedLine := min(m.feedCursor, 1)
	total := 0
	for _, source := range m.allFeeds {
		total += source.UnreadCount
	}
	lines = append(lines, m.menuLine(0, "All", total, width), m.menuLine(1, "Starred", -1, width), "")
	for i, source := range m.feeds {
		if m.feedCursor == i+2 {
			selectedLine = len(lines)
		}
		line := m.menuLine(i+2, source.Title, source.UnreadCount, width)
		if source.LastError != "" {
			line += m.styles.Dim.Render(" !")
			lines = append(lines, line, m.styles.Danger.Render("  "+truncate(source.LastError, max(1, width-2))))
			continue
		}
		lines = append(lines, line)
	}
	if len(m.feeds) == 0 && len(m.allFeeds) == 0 {
		lines = append(lines, m.styles.Dim.Render("Press a to add a feed."))
	} else if len(m.feeds) == 0 {
		lines = append(lines, m.styles.Dim.Render("No matching feeds."))
	}
	return strings.Join(visibleListLines(lines, selectedLine, m.listViewHeight()), "\n")
}

func (m Model) menuLine(index int, label string, count, width int) string {
	countText := ""
	if count >= 0 {
		countText = fmt.Sprintf(" %d", count)
	}
	line := truncate(label, max(1, width-utf8.RuneCountInString(countText))) + countText
	if m.active == feedsPane && m.feedCursor == index {
		return m.styles.Selected.Width(max(1, width)).Render(line)
	}
	return line
}

func (m Model) entriesView(width int) string {
	if len(m.entries) == 0 {
		return m.styles.Dim.Render("No matching articles.")
	}
	lines := make([]string, 0, len(m.entries)*2)
	selectedLine := 0
	for i, entry := range m.entries {
		if i == m.entryCursor {
			selectedLine = len(lines)
		}
		marker := "  "
		if !entry.Read {
			marker = "● "
		}
		star := ""
		if entry.Starred {
			star = " ★"
		}
		line := truncate(marker+entry.Title, max(1, width-utf8.RuneCountInString(star))) + star
		if m.active == articlesPane && i == m.entryCursor {
			line = m.styles.Selected.Width(max(1, width)).Render(line)
		}
		lines = append(lines, line, m.styles.Dim.Render("  "+truncate(entry.FeedTitle+" · "+relativeTime(entry.PublishedAt), max(1, width-2))))
	}
	return strings.Join(visibleListLines(lines, selectedLine, m.listViewHeight()), "\n")
}

// listViewHeight is the pane's inner height after its border and title line.
func (m Model) listViewHeight() int {
	return max(1, m.height-5)
}

// visibleListLines keeps the selected row and its detail line in view. Both
// feed errors and article metadata occupy the line immediately after a row.
func visibleListLines(lines []string, selectedLine, height int) []string {
	if len(lines) <= height {
		return lines
	}
	height = max(1, height)
	selectedEnd := min(selectedLine+1, len(lines)-1)
	start := clamp(selectedEnd-height+1, 0, len(lines)-height)
	start = min(start, selectedLine)
	return lines[start : start+height]
}

func (m Model) overlayView() string {
	var title, content string
	switch m.overlay {
	case quitOverlay:
		title = "Quit rxs?"
		content = "Are you sure you want to quit?\n\nPress y to quit or n to stay."
	case helpOverlay:
		title = "Help"
		content = "j/k or arrows  move / scroll\nh/l             change pane\ngg / G          beginning / end of list or article\nctrl+f / ctrl+b page down / up in feeds or reader\nctrl+d / ctrl+u half page down / up in reader\n/ then n / N    find in article; next / previous match\ntab / shift-tab select next / previous link in reader\nenter           open article or selected link\nspace           toggle read\ns               toggle starred\nr / R           refresh selected / all\n/               filter feeds or search articles\nu               show / hide read articles\na / d           add / remove feed\no               open original\ni / e           import / export OPML\nc               choose color scheme\nq               confirm quit\nesc             close dialog"
	case colorSchemeOverlay:
		title = "Color scheme"
		lines := make([]string, 0, len(m.schemeNames))
		for index, name := range m.schemeNames {
			line := "  " + name
			if index == m.schemeCursor {
				line = m.styles.Selected.Width(24).Render("› " + name)
			}
			lines = append(lines, line)
		}
		content = strings.Join(lines, "\n") + "\n\nj/k preview · Enter save · Esc cancel"
	case deleteOverlay:
		title = "Remove feed?"
		if m.feedCursor >= 2 && m.feedCursor-2 < len(m.feeds) {
			content = fmt.Sprintf("Remove %q and its downloaded articles?\n\nPress y to remove or n to cancel.", m.feeds[m.feedCursor-2].Title)
		}
	default:
		title = "Input"
		content = m.input.View() + "\n\nEnter to confirm · Esc to cancel"
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(m.styles.Scheme.Accent).
		Padding(1, 2).Width(max(20, min(76, m.width-6))).Render(m.styles.Selected.Render(title) + "\n\n" + content)
	return lipgloss.Place(max(1, m.width), max(1, m.height), lipgloss.Center, lipgloss.Center, box)
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "unknown date"
	}
	delta := time.Since(t)
	if delta < 0 {
		return t.Format("Jan 2, 2006")
	}
	switch {
	case delta < time.Minute:
		return "just now"
	case delta < time.Hour:
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(delta.Hours()))
	case delta < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(delta.Hours()/24))
	default:
		return t.Format("Jan 2, 2006")
	}
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func clamp(value, low, high int) int {
	if high < low {
		return low
	}
	return min(max(value, low), high)
}
