// Package app contains the Bubble Tea root model. It depends on service
// interfaces so terminal behavior can be tested without SQL or HTTP.
package app

import (
	"context"
	"fmt"
	"os/exec"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/polera/rxs/internal/domain"
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
	SetReadingProgress(context.Context, int64, float64) error
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
	feedFilterOverlay
	readerSearchOverlay
	deleteOverlay
	quitOverlay
	importOverlay
	exportOverlay
	helpOverlay
	colorSchemeOverlay
)

// ColorSchemeSaver persists the canonical name of a selected color scheme.
type ColorSchemeSaver func(string) error

type Model struct {
	store      Store
	refresher  Refresher
	browser    Browser
	tuiBrowser TUIBrowser
	styles     ui.Styles
	saveScheme ColorSchemeSaver

	allFeeds   []domain.Feed
	feeds      []domain.Feed
	entries    []domain.Entry
	filter     domain.EntryFilter
	feedFilter string

	feedCursor          int
	entryCursor         int
	readerEntry         *domain.Entry
	readerLinks         []render.Link
	readerLinkCursor    int
	readerSearch        string
	readerMatches       []readerMatch
	readerMatchCursor   int
	readerReachedBottom bool
	pendingG            bool
	active              pane
	overlay             overlay
	schemeNames         []string
	schemeCursor        int
	schemeOriginal      ui.Styles
	input               textinput.Model
	reader              viewport.Model

	width, height int
	busy          bool
	status        string
	errStatus     bool
	warningStatus string

	markReadOnScroll bool
}

type readerMatch struct {
	line       int
	start, end int
}

type loadedMsg struct {
	feeds   []domain.Feed
	entries []domain.Entry
	filter  domain.EntryFilter
	entryID int64
	err     error
}
type addMsg struct {
	feed domain.Feed
	err  error
}
type deleteMsg struct{ err error }
type stateMsg struct{ err error }
type readingProgressMsg struct{ err error }
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
type colorSchemeSavedMsg struct {
	name string
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

// SetColorSchemeSaver enables persistent color-scheme selection in the UI.
func (m *Model) SetColorSchemeSaver(saver ColorSchemeSaver) {
	m.saveScheme = saver
}

// SetMarkReadOnScroll controls whether moving away from an article preview
// marks that article as read.
func (m *Model) SetMarkReadOnScroll(enabled bool) {
	m.markReadOnScroll = enabled
}

// SetHideRead controls whether the initial article listing excludes read
// articles. The setting can still be toggled for the current session.
func (m *Model) SetHideRead(enabled bool) {
	m.filter.UnreadOnly = enabled
}

// SetWarningStatus displays a non-blocking warning until another status
// replaces it.
func (m *Model) SetWarningStatus(message string) {
	m.status, m.errStatus, m.warningStatus = message, false, message
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
	reader := viewport.New()
	model := Model{
		store: store, refresher: refresher, browser: browser, tuiBrowser: tuiBrowser,
		styles: styles, input: input, reader: reader, width: 100, height: 30,
		readerLinkCursor:  -1,
		readerMatchCursor: -1,
		active:            feedsPane, status: "Loading subscriptions…",
	}
	model.applyStyles(styles)
	model.resizeReader()
	return model
}

func (m Model) Init() tea.Cmd { return m.loadCmd() }

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeReader()
		m.checkReaderReachedBottom()
		return m, nil
	case loadedMsg:
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		if msg.filter != m.filter {
			return m, nil
		}
		m.allFeeds, m.entries = msg.feeds, msg.entries
		m.applyFeedSearch()
		m.reconcileFeedCursor()
		m.clampCursors()
		if msg.entryID != 0 {
			m.restoreEntrySelection(msg.entryID)
		}
		m.syncReader()
		if m.status == "Loading subscriptions…" || m.status == "Loading articles…" {
			m.status, m.errStatus = "Ready", false
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
		}
		return m, m.loadCmdPreserving(m.selectedEntryID())
	case readingProgressMsg:
		if msg.err != nil {
			m.setError(msg.err)
		}
		return m, nil
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
	case colorSchemeSavedMsg:
		if msg.err != nil {
			m.setError(fmt.Errorf("save color scheme: %w", msg.err))
		} else {
			m.status, m.errStatus = "Color scheme: "+msg.name, false
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
		m.checkReaderReachedBottom()
		return m, cmd
	}
	return m, nil
}
