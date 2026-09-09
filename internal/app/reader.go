package app

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/render"
)

// Only presentation inputs are cached, never the actionable reader snapshot.
// Wrapped text and spans are replaced rather than mutated across Model copies.
type readerRenderCache struct {
	entry     domain.Entry
	width     int
	dateLabel string
	wrapped   string
	linkSpans [][]readerMatch
	query     string
	pattern   *regexp.Regexp
}

func (m *Model) syncReader() {
	if m.active == readerPane && m.readerEntry != nil {
		m.setReaderContent(*m.readerEntry)
		return
	}
	if len(m.entries) == 0 {
		m.readerCache = readerRenderCache{}
		m.readerLinks = nil
		m.readerLinkCursor = -1
		m.readerMatches = nil
		m.readerMatchCursor = -1
		m.reader.SetContent("No article selected.")
		return
	}
	m.setReaderContent(m.entries[m.entryCursor])
}

func (m *Model) setReaderContent(entry domain.Entry) {
	m.renderReaderContent(entry)
}

func (m *Model) renderReaderContent(entry domain.Entry) {
	m.cacheReaderContent(entry, time.Now())
	m.paintReaderContent()
}

func (m *Model) cacheReaderContent(entry domain.Entry, now time.Time) {
	entry.Read, entry.Starred, entry.ReadingProgress = false, false, 0
	entry.EnrichmentInputHash = ""
	date := entry.PublishedAt
	if date.IsZero() {
		date = entry.UpdatedAt
	}
	dateLabel := relativeTimeAt(date, now)
	cached := &m.readerCache
	rebuild := cached.wrapped == "" || cached.entry != entry || cached.width != m.readerTextWidth() || cached.dateLabel != dateLabel
	if cached.entry.ID != entry.ID || cached.entry.HTML != entry.HTML || cached.entry.URL != entry.URL {
		m.readerLinkCursor = -1
	}
	if rebuild {
		cached.entry, cached.width = entry, m.readerTextWidth()
		cached.dateLabel = dateLabel
		cached.wrapped = m.wrapReaderContent(entry, dateLabel)
		cached.linkSpans = findReaderLinkSpans(cached.wrapped, m.readerLinks)
	}
	queryChanged := cached.query != m.readerSearch
	if queryChanged {
		cached.query = m.readerSearch
		cached.pattern = nil
		if cached.query != "" {
			cached.pattern, _ = regexp.Compile("(?i)" + regexp.QuoteMeta(cached.query))
		}
	}
	if rebuild || queryChanged {
		m.readerMatches = findReaderMatches(cached.wrapped, cached.pattern)
	}
	if len(m.readerMatches) == 0 {
		m.readerMatchCursor = -1
	} else {
		m.readerMatchCursor = clamp(m.readerMatchCursor, 0, len(m.readerMatches)-1)
	}
}

func (m *Model) wrapReaderContent(entry domain.Entry, dateLabel string) string {
	metadata := []string{entry.Author, dateLabel, entry.FeedTitle}
	if entry.ContentSource == domain.ContentSourceFullArticle {
		metadata = append(metadata, "full text")
	}
	meta := strings.Trim(strings.Join(metadata, " · "), " ·")
	content := entry.Text
	m.readerLinks = nil
	if entry.HTML != "" {
		content, m.readerLinks = render.TextWithLinks(entry.HTML, entry.URL, func(index int, link render.Link, text string) string {
			linkID := fmt.Sprintf("id=rxs-link-%d", index)
			return m.styles.Link.Hyperlink(link.URL, linkID).Render(text)
		})
	}
	if content == "" {
		content = "This feed did not include article content. Press o to open the original."
	}
	content = m.styleReaderHeadings(content)
	article := m.styles.Selected.Render(entry.Title) + "\n" + m.styles.Dim.Render(meta) + "\n\n" + content
	return lipgloss.Wrap(article, m.readerTextWidth(), " ")
}

func (m *Model) paintReaderContent() {
	wrapped := m.readerCache.wrapped
	if m.readerLinkCursor >= 0 && m.readerLinkCursor < len(m.readerCache.linkSpans) {
		lines := strings.Split(wrapped, "\n")
		for _, span := range m.readerCache.linkSpans[m.readerLinkCursor] {
			lines[span.line] = styleReaderRanges(lines[span.line], []lipgloss.Range{lipgloss.NewRange(span.start, span.end, m.styles.Selected)})
		}
		wrapped = strings.Join(lines, "\n")
	}
	if len(m.readerMatches) > 0 {
		wrapped = m.highlightReaderMatches(wrapped, m.readerMatches, m.readerMatchCursor)
	}
	m.reader.SetContent(wrapped)
}

func (m Model) styleReaderHeadings(content string) string {
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		plain := ansi.Strip(line)
		level := 0
		for level < len(plain) && level < 6 && plain[level] == '#' {
			level++
		}
		if level >= 2 && level < len(plain) && plain[level] == ' ' {
			lines[index] = m.styles.Heading.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

func (m *Model) searchReader(query string) {
	m.readerSearch = query
	m.readerMatchCursor = 0
	m.cacheReaderContent(m.currentReaderEntry(), time.Now())
	if len(m.readerMatches) == 0 {
		m.paintReaderContent()
		m.setStatus(fmt.Sprintf("No matches for %q", query), true)
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
			m.setStatus(fmt.Sprintf("No matches for %q", m.readerSearch), true)
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
	m.paintReaderContent()
	m.reader.EnsureVisible(match.line, match.start, match.end)
	m.setStatus(fmt.Sprintf("Match %d/%d: %s", m.readerMatchCursor+1, len(m.readerMatches), m.readerSearch), false)
	m.checkReaderReachedBottom()
}

func findReaderMatches(content string, pattern *regexp.Regexp) []readerMatch {
	if pattern == nil {
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
		lines[line] = styleReaderRanges(lines[line], ranges)
	}
	return strings.Join(lines, "\n")
}

// StyleRanges replaces highlighted text with stripped text. Split ranges at
// OSC boundaries and explicitly retain the hyperlink on each replacement.
func styleReaderRanges(line string, ranges []lipgloss.Range) string {
	var linked []lipgloss.Range
	var state byte
	var url, params, previousURL, previousParams string
	column, current, previous := 0, 0, -1
	for remaining := line; len(remaining) > 0; {
		sequence, width, n, next := ansi.DecodeSequence(remaining, state, nil)
		remaining, state = remaining[n:], next
		if strings.HasPrefix(sequence, "\x1b]8;") {
			payload := strings.TrimSuffix(strings.TrimSuffix(sequence[4:], "\a"), "\x1b\\")
			params, url, _ = strings.Cut(payload, ";")
		}
		if width == 0 {
			continue
		}
		for current < len(ranges) && ranges[current].End <= column {
			current++
		}
		if current == len(ranges) {
			break
		}
		if ranges[current].Start < column+width {
			if previous == current && previousURL == url && previousParams == params && linked[len(linked)-1].End == column {
				linked[len(linked)-1].End = column + width
			} else {
				style := ranges[current].Style
				if url != "" {
					style = style.Hyperlink(url, params)
				}
				linked = append(linked, lipgloss.NewRange(column, column+width, style))
			}
			previous, previousURL, previousParams = current, url, params
		}
		column += width
	}
	return lipgloss.StyleRanges(line, linked...)
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
	m.setStatus(fmt.Sprintf("Link %d/%d: %s", m.readerLinkCursor+1, len(m.readerLinks), link.Text), false)
	m.paintReaderContent()
	m.ensureReaderLinkVisible(m.readerLinkCursor)
	m.checkReaderReachedBottom()
}

func (m *Model) ensureReaderLinkVisible(index int) {
	if spans := m.readerCache.linkSpans[index]; len(spans) > 0 {
		span := spans[0]
		m.reader.EnsureVisible(span.line, span.start, span.start+1)
	}
}

// OSC identities survive wrapping (including links split across lines). Decode
// once so Tab uses cell ranges without reparsing HTML or searching styled text.
func findReaderLinkSpans(content string, links []render.Link) [][]readerMatch {
	if len(links) == 0 {
		return nil
	}
	markers := make(map[string]int, len(links))
	for index, link := range links {
		markers[ansi.SetHyperlink(link.URL, fmt.Sprintf("id=rxs-link-%d", index))] = index
	}
	spans := make([][]readerMatch, len(links))
	active, line, column := -1, 0, 0
	var state byte
	for len(content) > 0 {
		sequence, width, n, next := ansi.DecodeSequence(content, state, nil)
		content, state = content[n:], next
		switch {
		case sequence == "\n":
			line++
			column = 0
		case strings.HasPrefix(sequence, "\x1b]8;"):
			active = -1
			if index, ok := markers[sequence]; ok {
				active = index
			}
		case width > 0:
			if active >= 0 {
				previous := len(spans[active]) - 1
				if previous >= 0 && spans[active][previous].line == line && spans[active][previous].end == column {
					spans[active][previous].end += width
				} else {
					spans[active] = append(spans[active], readerMatch{line: line, start: column, end: column + width})
				}
			}
			column += width
		}
	}
	return spans
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
	if entry.ID != 0 || entry.Title != "" || entry.Text != "" || entry.HTML != "" {
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
