// Package app contains the Bubble Tea root model. It depends on service
// interfaces so terminal behavior can be tested without SQL or HTTP.
package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/polera/rxs/internal/domain"
	"github.com/polera/rxs/internal/opml"
	"github.com/polera/rxs/internal/render"
	"github.com/polera/rxs/internal/ui"
)

type Store interface {
	AddFeed(context.Context, string) (domain.Feed, error)
	DeleteFeed(context.Context, int64) error
	Feeds(context.Context) ([]domain.Feed, error)
	Entries(context.Context, domain.EntryFilter) ([]domain.Entry, error)
	SetRead(context.Context, int64, bool) error
	SetStarred(context.Context, int64, bool) error
}

type Refresher interface {
	Refresh(context.Context, int64) domain.RefreshResult
	RefreshAll(context.Context, []domain.Feed, int) []domain.RefreshResult
}

type Browser func(string) error
type TUIBrowser func(string) (*exec.Cmd, error)

const (
	maxReaderTextWidth      = 88
	readerHorizontalPadding = 2
)

type pane int

const (
	feedsPane pane = iota
	articlesPane
	readerPane
)

type overlay int

const (
	noOverlay overlay = iota
	addOverlay
	searchOverlay
	readerSearchOverlay
	deleteOverlay
	importOverlay
	exportOverlay
	helpOverlay
)

type Model struct {
	store      Store
	refresher  Refresher
	browser    Browser
	tuiBrowser TUIBrowser
	styles     ui.Styles

	feeds   []domain.Feed
	entries []domain.Entry
	filter  domain.EntryFilter

	feedCursor        int
	entryCursor       int
	readerEntry       *domain.Entry
	readerLinks       []render.Link
	readerLinkCursor  int
	readerSearch      string
	readerMatches     []readerMatch
	readerMatchCursor int
	readerPendingG    bool
	active            pane
	overlay           overlay
	input             textinput.Model
	reader            viewport.Model

	width, height int
	busy          bool
	status        string
	errStatus     bool
}

type readerMatch struct {
	line       int
	start, end int
}

type loadedMsg struct {
	feeds   []domain.Feed
	entries []domain.Entry
	filter  domain.EntryFilter
	err     error
}
type addMsg struct {
	feed domain.Feed
	err  error
}
type deleteMsg struct{ err error }
type stateMsg struct{ err error }
type refreshMsg struct{ results []domain.RefreshResult }
type browserMsg struct {
	target string
	err    error
}
type importMsg struct {
	count int
	err   error
}
type exportMsg struct {
	path string
	err  error
}

func New(store Store, refresher Refresher, browser Browser, styles ...ui.Styles) Model {
	return newModel(store, refresher, browser, nil, modelStyles(styles))
}

// NewWithTUIBrowser configures an interactive browser that temporarily takes
// over the terminal and returns control to the feed reader when it exits.
func NewWithTUIBrowser(store Store, refresher Refresher, browser TUIBrowser, styles ...ui.Styles) Model {
	return newModel(store, refresher, nil, browser, modelStyles(styles))
}

func modelStyles(configured []ui.Styles) ui.Styles {
	if len(configured) > 0 {
		return configured[0]
	}
	styles, err := ui.ResolveScheme(ui.DefaultScheme)
	if err != nil {
		panic(err)
	}
	return styles
}

func newModel(store Store, refresher Refresher, browser Browser, tuiBrowser TUIBrowser, styles ui.Styles) Model {
	input := textinput.New()
	input.SetWidth(60)
	inputStyles := input.Styles()
	inputStyles.Focused.Text = styles.Base
	inputStyles.Focused.Placeholder = styles.Dim
	inputStyles.Focused.Suggestion = styles.Dim
	inputStyles.Focused.Prompt = lipgloss.NewStyle().Foreground(styles.Scheme.Accent)
	inputStyles.Blurred.Text = styles.Dim
	inputStyles.Blurred.Placeholder = styles.Dim
	inputStyles.Blurred.Suggestion = styles.Dim
	inputStyles.Blurred.Prompt = styles.Dim
	inputStyles.Cursor.Color = styles.Scheme.Accent
	input.SetStyles(inputStyles)
	reader := viewport.New()
	model := Model{
		store: store, refresher: refresher, browser: browser, tuiBrowser: tuiBrowser,
		styles: styles, input: input, reader: reader, width: 100, height: 30,
		readerLinkCursor:  -1,
		readerMatchCursor: -1,
		active:            feedsPane, status: "Loading subscriptions…",
	}
	model.resizeReader()
	return model
}

func (m Model) Init() tea.Cmd { return m.loadCmd() }

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeReader()
		return m, nil
	case loadedMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		if msg.filter != m.filter {
			return m, nil
		}
		m.feeds, m.entries = msg.feeds, msg.entries
		m.reconcileFeedCursor()
		m.clampCursors()
		m.syncReader()
		if m.status == "Loading subscriptions…" {
			m.status = "Ready"
		}
		return m, nil
	case addMsg:
		m.busy = false
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		m.status, m.errStatus = "Added "+msg.feed.Title+"; refreshing…", false
		m.busy = true
		return m, m.refreshOneCmd(msg.feed.ID)
	case deleteMsg:
		m.busy = false
		if msg.err != nil {
			m.setError(msg.err)
		} else {
			m.status, m.errStatus = "Feed removed", false
			m.feedCursor, m.entryCursor = 0, 0
			m.filter.FeedID, m.filter.StarredOnly = 0, false
			m.readerEntry = nil
			m.active = feedsPane
		}
		return m, m.loadCmd()
	case stateMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, m.loadCmd()
		}
		return m, m.loadCmd()
	case refreshMsg:
		m.busy = false
		failures, added := 0, 0
		var lastErr error
		for _, result := range msg.results {
			added += result.Added
			if result.Err != nil {
				failures++
				lastErr = result.Err
			}
		}
		if failures > 0 {
			m.status = fmt.Sprintf("Refresh finished: %d new, %d failed: %v", added, failures, lastErr)
			m.errStatus = true
		} else {
			m.status = fmt.Sprintf("Refresh finished: %d new article(s)", added)
			m.errStatus = false
		}
		return m, m.loadCmd()
	case browserMsg:
		if msg.err != nil {
			m.setError(msg.err)
		} else {
			m.status, m.errStatus = "Opened "+msg.target, false
		}
		return m, nil
	case importMsg:
		m.busy = false
		if msg.err != nil {
			m.setError(msg.err)
		} else {
			m.status, m.errStatus = fmt.Sprintf("Imported %d subscription(s)", msg.count), false
		}
		return m, m.loadCmd()
	case exportMsg:
		m.busy = false
		if msg.err != nil {
			m.setError(msg.err)
		} else {
			m.status, m.errStatus = "Exported subscriptions to "+msg.path, false
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.overlay != noOverlay {
			return m.updateOverlay(msg)
		}
		return m.updateKey(msg)
	}
	if m.active == readerPane {
		var cmd tea.Cmd
		m.reader, cmd = m.reader.Update(message)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.active == readerPane {
		if m.readerPendingG {
			m.readerPendingG = false
			if key == "g" {
				m.reader.GotoTop()
				m.status, m.errStatus = "Beginning of article", false
				return m, nil
			}
		}
		switch key {
		case "/":
			return m.openInput(readerSearchOverlay, "Find in article", "search text")
		case "g":
			m.readerPendingG = true
			return m, nil
		case "G":
			m.reader.GotoBottom()
			m.status, m.errStatus = "End of article", false
			return m, nil
		case "ctrl+f":
			m.reader.PageDown()
			return m, nil
		case "ctrl+b":
			m.reader.PageUp()
			return m, nil
		case "ctrl+d":
			m.reader.HalfPageDown()
			return m, nil
		case "ctrl+u":
			m.reader.HalfPageUp()
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
		return m, tea.Quit
	case "?":
		m.overlay = helpOverlay
		return m, nil
	case "a":
		return m.openInput(addOverlay, "Feed URL", "https://example.com/feed.xml")
	case "/":
		return m.openInput(searchOverlay, "Search", "title or article text")
	case "i":
		return m.openInput(importOverlay, "Import OPML", "path/to/subscriptions.opml")
	case "e":
		return m.openInput(exportOverlay, "Export OPML", "rxs-subscriptions.opml")
	case "h", "left":
		if m.active > feedsPane {
			m.active--
			m.resizeReader()
		}
		return m, nil
	case "shift+tab":
		if m.active == readerPane && len(m.readerLinks) > 0 {
			m.selectReaderLink(-1)
			return m, nil
		}
		if m.active > feedsPane {
			m.active--
			m.resizeReader()
		}
		return m, nil
	case "l", "right":
		if m.active < readerPane {
			m.active++
			m.resizeReader()
		}
		return m, nil
	case "tab":
		if m.active == readerPane && len(m.readerLinks) > 0 {
			m.selectReaderLink(1)
			return m, nil
		}
		if m.active < readerPane {
			m.active++
			m.resizeReader()
		}
		return m, nil
	case "esc":
		if m.active == readerPane && m.readerLinkCursor >= 0 {
			m.readerLinkCursor = -1
			m.renderReaderContent(m.currentReaderEntry())
		}
		return m, nil
	case "j", "down":
		oldFeed := m.feedCursor
		m.move(1)
		if m.active == feedsPane && oldFeed != m.feedCursor {
			return m, m.loadCmd()
		}
		return m, nil
	case "k", "up":
		oldFeed := m.feedCursor
		m.move(-1)
		if m.active == feedsPane && oldFeed != m.feedCursor {
			return m, m.loadCmd()
		}
		return m, nil
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
			m.status = "Showing unread articles"
		} else {
			m.status = "Showing all read states"
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
		return m, cmd
	}
	return m, nil
}

func (m Model) updateOverlay(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
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
			m.status, m.errStatus = "Article search cleared", false
			return m, nil
		}
		if value == "" && mode == searchOverlay {
			m.filter.Search = ""
			m.status, m.errStatus = "Search cleared", false
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

func (m *Model) openInput(mode overlay, prompt, placeholder string) (tea.Model, tea.Cmd) {
	m.overlay = mode
	m.input.Reset()
	m.input.Prompt = prompt + ": "
	m.input.Placeholder = placeholder
	m.input.SetWidth(max(20, min(70, m.width-10)))
	return *m, m.input.Focus()
}

func (m *Model) closeOverlay() {
	m.overlay = noOverlay
	m.input.Blur()
}

func (m *Model) move(delta int) {
	switch m.active {
	case feedsPane:
		old := m.feedCursor
		m.feedCursor = clamp(m.feedCursor+delta, 0, len(m.feeds)+1)
		if old != m.feedCursor {
			m.entryCursor = 0
			m.applyFeedFilter()
			m.status = "Loading articles…"
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
	}
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
	entry := &m.entries[m.entryCursor]
	m.active = readerPane
	m.resizeReader()
	opened := *entry
	m.readerEntry = &opened
	m.readerSearch = ""
	m.readerMatches = nil
	m.readerMatchCursor = -1
	m.readerPendingG = false
	m.setReaderContent(opened)
	m.reader.GotoTop()
	if !entry.Read {
		entry.Read = true
		return m, func() tea.Msg { return stateMsg{err: m.store.SetRead(context.Background(), entry.ID, true)} }
	}
	return m, nil
}

func (m Model) toggleRead() (tea.Model, tea.Cmd) {
	if len(m.entries) == 0 || m.active == feedsPane {
		return m, nil
	}
	entry := &m.entries[m.entryCursor]
	entry.Read = !entry.Read
	if m.readerEntry != nil && m.readerEntry.ID == entry.ID {
		m.readerEntry.Read = entry.Read
	}
	value, id := entry.Read, entry.ID
	return m, func() tea.Msg { return stateMsg{err: m.store.SetRead(context.Background(), id, value)} }
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
	if m.busy || len(m.feeds) == 0 {
		return m, nil
	}
	m.busy = true
	m.status = fmt.Sprintf("Refreshing %d feed(s)…", len(m.feeds))
	feeds := append([]domain.Feed(nil), m.feeds...)
	return m, func() tea.Msg {
		return refreshMsg{results: m.refresher.RefreshAll(context.Background(), feeds, 4)}
	}
}

func (m Model) refreshOneCmd(id int64) tea.Cmd {
	return func() tea.Msg {
		return refreshMsg{results: []domain.RefreshResult{m.refresher.Refresh(context.Background(), id)}}
	}
}

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
	filter := m.filter
	return func() tea.Msg {
		feeds, err := m.store.Feeds(context.Background())
		if err != nil {
			return loadedMsg{filter: filter, err: err}
		}
		entries, err := m.store.Entries(context.Background(), filter)
		return loadedMsg{feeds: feeds, entries: entries, filter: filter, err: err}
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
	meta := strings.Trim(strings.Join([]string{entry.Author, relativeTime(date), entry.FeedTitle}, " · "), " ·")
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
}

func (m Model) readerTextWidth() int {
	return max(1, m.reader.Width()-m.reader.Style.GetHorizontalFrameSize())
}

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
	}
	statusText := m.status
	if m.busy {
		statusText = "◌ " + statusText
	}
	status := statusStyle.Render(truncate(statusText, max(1, m.width-2)))
	keyText := "j/k move · h/l pane · enter read · r refresh · / search · ? help"
	if m.active == readerPane {
		keyText = "j/k scroll · ctrl+f/b page · gg/G start/end · / find · n/N matches · h articles · ? help"
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
	total := 0
	for _, source := range m.feeds {
		total += source.UnreadCount
	}
	lines = append(lines, m.menuLine(0, "All", total, width), m.menuLine(1, "Starred", -1, width), "")
	for i, source := range m.feeds {
		line := m.menuLine(i+2, source.Title, source.UnreadCount, width)
		if source.LastError != "" {
			line += m.styles.Dim.Render(" !")
			lines = append(lines, line, m.styles.Danger.Render("  "+truncate(source.LastError, max(1, width-2))))
			continue
		}
		lines = append(lines, line)
	}
	if len(m.feeds) == 0 {
		lines = append(lines, m.styles.Dim.Render("Press a to add a feed."))
	}
	return strings.Join(lines, "\n")
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
	for i, entry := range m.entries {
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
	return strings.Join(lines, "\n")
}

func (m Model) overlayView() string {
	var title, content string
	switch m.overlay {
	case helpOverlay:
		title = "Help"
		content = "j/k or arrows  move / scroll\nh/l             change pane\ngg / G          beginning / end of article\nctrl+f / ctrl+b page down / up in reader\nctrl+d / ctrl+u half page down / up in reader\n/ then n / N    find in article; next / previous match\ntab / shift-tab select next / previous link in reader\nenter           open article or selected link\nspace           toggle read\ns               toggle starred\nr / R           refresh selected / all\n/               search downloaded articles outside reader\nu               unread filter\na / d           add / remove feed\no               open original\ni / e           import / export OPML\nq or esc        close / quit"
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
