// Package app contains the Bubble Tea root model. It depends on service
// interfaces so terminal behavior can be tested without SQL or HTTP.
package app

import (
	"context"
	"fmt"
	"os/exec"
	"time"

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
	statusLifetime          = 10 * time.Second
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

	allFeeds              []domain.Feed
	feeds                 []domain.Feed
	entries               []domain.Entry
	filter                domain.EntryFilter
	feedFilter            string
	loadGeneration        uint64
	hasLoaded             bool
	initialRefreshPending bool
	stateWrites           []stateWrite
	stateRevision         uint64
	quitting              bool
	deleteTarget          domain.Feed
	deleting              bool

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

	width, height      int
	busy               bool
	status             string
	errStatus          bool
	warningStatus      string
	statusGeneration   uint64
	statusTimerPending bool

	markReadOnScroll bool
}

type readerMatch struct {
	line       int
	start, end int
}

type loadedMsg struct {
	feeds             []domain.Feed
	entries           []domain.Entry
	filter            domain.EntryFilter
	generation        uint64
	preserveSelection bool
	initial           bool
	err               error
}
type addMsg struct {
	feed domain.Feed
	err  error
}
type deleteMsg struct{ err error }
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
type statusTimeoutMsg struct{ generation uint64 }

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
	m.setPersistentStatus(message, false)
	m.warningStatus = message
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

func (m Model) Init() tea.Cmd { return m.loadCmdWithOptions(false, true) }

func (m Model) Update(message tea.Msg) (next tea.Model, cmd tea.Cmd) {
	defer func() {
		updated, ok := next.(Model)
		if !ok || !updated.statusTimerPending {
			return
		}
		updated.statusTimerPending = false
		generation := updated.statusGeneration
		next = updated
		timer := tea.Tick(statusLifetime, func(time.Time) tea.Msg {
			return statusTimeoutMsg{generation: generation}
		})
		cmd = tea.Batch(cmd, timer)
	}()

	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeReader()
		m.checkReaderReachedBottom()
		return m, nil
	case loadedMsg:
		if msg.initial {
			m.initialRefreshPending = true
		}
		if msg.generation != m.loadGeneration || msg.filter != m.filter {
			return m.finishInitialRefresh()
		}
		if msg.err != nil {
			m.setError(msg.err)
			return m, nil
		}
		selectedID := m.selectedEntryID()
		m.allFeeds, m.entries = msg.feeds, msg.entries
		m.hasLoaded = true
		m.applyFeedSearch()
		m.reconcileFeedCursor()
		m.clampCursors()
		if msg.preserveSelection {
			m.restoreEntrySelection(selectedID)
		}
		if m.readerEntry != nil {
			for _, entry := range m.entries {
				if entry.ID == m.readerEntry.ID {
					m.applyEntryState(entry.ID, stateOf(entry), readField|starredField|progressField)
					break
				}
			}
		}
		for _, write := range m.stateWrites {
			m.applyEntryState(write.entryID, write.after, write.fields)
		}
		m.syncReader()
		if m.status == "Loading subscriptions…" || m.status == "Loading articles…" {
			m.clearStatus()
		}
		return m.finishInitialRefresh()
	case addMsg:
		m.busy = false
		if msg.err != nil {
			m.setError(msg.err)
			return m.finishInitialRefresh()
		}
		m.setPersistentStatus("Added "+msg.feed.Title+"; refreshing…", false)
		m.busy = true
		return m, m.refreshOneCmd(msg.feed.ID)
	case deleteMsg:
		m.deleting = false
		m.busy = false
		if msg.err != nil {
			m.setError(msg.err)
		} else {
			m.setStatus("Feed removed", false)
			m.feedCursor, m.entryCursor = 0, 0
			m.filter.FeedID, m.filter.StarredOnly = 0, false
			m.readerEntry = nil
			m.active = feedsPane
		}
		return m, m.loadCmd()
	case stateMsg:
		return m.completeStateWrite(msg)
	case refreshMsg:
		m.busy = false
		failures, added, expanded, expansionFailures := 0, 0, 0, 0
		var lastErr error
		for _, result := range msg.results {
			added += result.Added
			expanded += result.Expanded
			expansionFailures += result.ExpansionFailed
			if result.Err != nil {
				failures++
				lastErr = result.Err
			}
		}
		if failures > 0 {
			if expanded > 0 || expansionFailures > 0 {
				m.setStatus(fmt.Sprintf("Refresh finished: %d new, %d expanded, %d full-text fetch unavailable, %d feed(s) failed: %v", added, expanded, expansionFailures, failures, lastErr), true)
			} else {
				m.setStatus(fmt.Sprintf("Refresh finished: %d new, %d failed: %v", added, failures, lastErr), true)
			}
		} else if expanded > 0 || expansionFailures > 0 {
			m.setStatus(fmt.Sprintf("Refresh finished: %d new, %d expanded, %d full-text fetch unavailable", added, expanded, expansionFailures), false)
		} else {
			m.setStatus(fmt.Sprintf("Refresh finished: %d new article(s)", added), false)
		}
		return m, m.loadCmdPreserving()
	case browserMsg:
		if msg.err != nil {
			m.setError(msg.err)
		} else {
			m.setStatus("Opened "+msg.target, false)
		}
		return m, nil
	case importMsg:
		m.busy = false
		if msg.err != nil {
			m.setError(msg.err)
		} else {
			m.setStatus(fmt.Sprintf("Imported %d subscription(s)", msg.count), false)
		}
		return m, m.loadCmdPreserving()
	case exportMsg:
		m.busy = false
		if msg.err != nil {
			m.setError(msg.err)
		} else {
			m.setStatus("Exported subscriptions to "+msg.path, false)
		}
		return m.finishInitialRefresh()
	case colorSchemeSavedMsg:
		if msg.err != nil {
			m.setError(fmt.Errorf("save color scheme: %w", msg.err))
		} else {
			m.setStatus("Color scheme: "+msg.name, false)
		}
		return m, nil
	case statusTimeoutMsg:
		if msg.generation == m.statusGeneration {
			m.clearStatus()
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.quitting {
			return m, nil
		}
		if m.overlay != noOverlay {
			return m.updateOverlay(msg)
		}
		return m.updateKey(msg)
	}
	if m.overlay != noOverlay {
		return m.updateOverlay(message)
	}
	if m.active == readerPane {
		var cmd tea.Cmd
		m.reader, cmd = m.reader.Update(message)
		m.checkReaderReachedBottom()
		return m, cmd
	}
	return m, nil
}

func (m *Model) setStatus(message string, isError bool) {
	m.status, m.errStatus = message, isError
	m.statusGeneration++
	m.statusTimerPending = message != ""
}

func (m *Model) setPersistentStatus(message string, isError bool) {
	m.status, m.errStatus = message, isError
	m.statusGeneration++
	m.statusTimerPending = false
}

func (m *Model) clearStatus() {
	m.setPersistentStatus("", false)
}
