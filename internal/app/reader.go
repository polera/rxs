package app

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/render"
)

func (m *Model) syncReader() {
	if m.active == readerPane && m.readerEntry != nil {
		m.setReaderContent(*m.readerEntry)
		return
	}
	if len(m.entries) == 0 {
		m.readerLinks = nil
		m.readerLinkCursor = -1
		m.reader.SetContent("No article selected.")
		return
	}
	m.setReaderContent(m.entries[m.entryCursor])
}

func (m *Model) setReaderContent(entry domain.Entry) {
	_, m.readerLinks = render.TextWithLinks(entry.HTML, entry.URL, nil)
	m.readerLinkCursor = -1
	m.renderReaderContent(entry)
}

func (m *Model) renderReaderContent(entry domain.Entry) {
	date := entry.PublishedAt
	if date.IsZero() {
		date = entry.UpdatedAt
	}
	metadata := []string{entry.Author, relativeTime(date), entry.FeedTitle}
	if entry.ContentSource == domain.ContentSourceFullArticle {
		metadata = append(metadata, "full text")
	}
	meta := strings.Trim(strings.Join(metadata, " · "), " ·")
	content := entry.Text
	if content == "" {
		content = "This feed did not include article content. Press o to open the original."
	}
	if len(m.readerLinks) > 0 {
		linkedContent, _ := render.TextWithLinks(entry.HTML, entry.URL, func(index int, link render.Link, text string) string {
			linkID := fmt.Sprintf("id=rxs-link-%d", index)
			style := m.styles.Link.Hyperlink(link.URL, linkID)
			if index == m.readerLinkCursor {
				style = m.styles.Selected.Hyperlink(link.URL, linkID)
			}
			return style.Render(text)
		})
		content = linkedContent
	}
	article := m.styles.Selected.Render(entry.Title) + "\n" + m.styles.Dim.Render(meta) + "\n\n" + content
	wrapped := lipgloss.Wrap(article, m.readerTextWidth(), " ")
	m.readerMatches = findReaderMatches(wrapped, m.readerSearch)
	if len(m.readerMatches) == 0 {
		m.readerMatchCursor = -1
	} else {
		m.readerMatchCursor = clamp(m.readerMatchCursor, 0, len(m.readerMatches)-1)
		wrapped = m.highlightReaderMatches(wrapped, m.readerMatches, m.readerMatchCursor)
	}
	m.reader.SetContent(wrapped)
}

func (m *Model) searchReader(query string) {
	m.readerSearch = query
	m.readerMatchCursor = 0
	m.renderReaderContent(m.currentReaderEntry())
	if len(m.readerMatches) == 0 {
		m.status, m.errStatus = fmt.Sprintf("No matches for %q", query), true
		return
	}
	for index, match := range m.readerMatches {
		if match.line >= m.reader.YOffset() {
			m.readerMatchCursor = index
			break
		}
	}
	m.showReaderMatch()
	m.checkReaderReachedBottom()
}

func (m *Model) selectReaderMatch(delta int) {
	if len(m.readerMatches) == 0 {
		if m.readerSearch != "" {
			m.status, m.errStatus = fmt.Sprintf("No matches for %q", m.readerSearch), true
		}
		return
	}
	m.readerMatchCursor = (m.readerMatchCursor + delta + len(m.readerMatches)) % len(m.readerMatches)
	m.showReaderMatch()
}

func (m *Model) showReaderMatch() {
	if m.readerMatchCursor < 0 || m.readerMatchCursor >= len(m.readerMatches) {
		return
	}
	match := m.readerMatches[m.readerMatchCursor]
	m.renderReaderContent(m.currentReaderEntry())
	m.reader.EnsureVisible(match.line, match.start, match.end)
	m.status, m.errStatus = fmt.Sprintf("Match %d/%d: %s", m.readerMatchCursor+1, len(m.readerMatches), m.readerSearch), false
	m.checkReaderReachedBottom()
}

func findReaderMatches(content, query string) []readerMatch {
	if query == "" {
		return nil
	}
	pattern, err := regexp.Compile("(?i)" + regexp.QuoteMeta(query))
	if err != nil {
		return nil
	}
	var matches []readerMatch
	for lineNumber, line := range strings.Split(content, "\n") {
		plain := ansi.Strip(line)
		for _, indexes := range pattern.FindAllStringIndex(plain, -1) {
			start := lipgloss.Width(plain[:indexes[0]])
			matches = append(matches, readerMatch{
				line:  lineNumber,
				start: start,
				end:   start + lipgloss.Width(plain[indexes[0]:indexes[1]]),
			})
		}
	}
	return matches
}

func (m Model) highlightReaderMatches(content string, matches []readerMatch, selected int) string {
	lines := strings.Split(content, "\n")
	byLine := make(map[int][]lipgloss.Range)
	for index, match := range matches {
		style := m.styles.SearchMatch
		if index == selected {
			style = m.styles.Selected
		}
		byLine[match.line] = append(byLine[match.line], lipgloss.NewRange(match.start, match.end, style))
	}
	for line, ranges := range byLine {
		lines[line] = lipgloss.StyleRanges(lines[line], ranges...)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) selectReaderLink(delta int) {
	if len(m.readerLinks) == 0 {
		return
	}
	if m.readerLinkCursor < 0 {
		if delta < 0 {
			m.readerLinkCursor = len(m.readerLinks) - 1
		} else {
			m.readerLinkCursor = 0
		}
	} else {
		m.readerLinkCursor = (m.readerLinkCursor + delta + len(m.readerLinks)) % len(m.readerLinks)
	}
	link := m.readerLinks[m.readerLinkCursor]
	m.status, m.errStatus = fmt.Sprintf("Link %d/%d: %s", m.readerLinkCursor+1, len(m.readerLinks), link.Text), false
	m.renderReaderContent(m.currentReaderEntry())
	m.ensureReaderLinkVisible(m.readerLinkCursor, link)
	m.checkReaderReachedBottom()
}

func (m *Model) ensureReaderLinkVisible(index int, link render.Link) {
	marker := ansi.SetHyperlink(link.URL, fmt.Sprintf("id=rxs-link-%d", index))
	for lineNumber, line := range strings.Split(m.reader.GetContent(), "\n") {
		markerAt := strings.Index(line, marker)
		if markerAt < 0 {
			continue
		}
		start := lipgloss.Width(ansi.Strip(line[:markerAt]))
		m.reader.EnsureVisible(lineNumber, start, start+1)
		return
	}
}

func (m Model) currentReaderEntry() domain.Entry {
	if m.readerEntry != nil {
		return *m.readerEntry
	}
	if m.entryCursor >= 0 && m.entryCursor < len(m.entries) {
		return m.entries[m.entryCursor]
	}
	return domain.Entry{}
}

func (m *Model) resizeReader() {
	height := max(3, m.height-3)
	width := m.width
	if m.active == readerPane {
		width = m.width
	} else if m.width >= 110 {
		width = m.width - m.width/4 - m.width/3
	} else if m.width >= 70 {
		width = m.width / 2
	}
	viewportWidth := max(10, width-4)
	textWidth := min(maxReaderTextWidth, max(1, viewportWidth-2*readerHorizontalPadding))
	remaining := viewportWidth - textWidth
	m.reader.Style = lipgloss.NewStyle().
		PaddingLeft(remaining / 2).
		PaddingRight(remaining - remaining/2)
	m.reader.SetWidth(viewportWidth)
	m.reader.SetHeight(max(3, height-4))

	// Content is wrapped before it reaches the viewport so lines break at word
	// boundaries instead of being sliced at an arbitrary terminal column.
	entry := m.currentReaderEntry()
	if entry.ID != 0 || entry.Title != "" || entry.Text != "" {
		m.renderReaderContent(entry)
	}
	m.checkReaderReachedBottom()
}

func (m Model) readerTextWidth() int {
	return max(1, m.reader.Width()-m.reader.Style.GetHorizontalFrameSize())
}

func (m *Model) restoreReaderProgress(progress float64) {
	maxOffset := max(0, m.reader.TotalLineCount()-m.reader.Height()+m.reader.Style.GetVerticalFrameSize())
	m.reader.SetYOffset(int(math.Round(float64(maxOffset) * max(0, min(1, progress)))))
}

func (m *Model) checkReaderReachedBottom() {
	if m.active == readerPane && m.readerEntry != nil && m.reader.AtBottom() {
		m.readerReachedBottom = true
	}
}
