package app

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/polera/rxs/internal/domain"
)

type lastViewTask struct {
	run     func(context.Context) error
	started bool
	result  *lastViewMsg
}

type lastViewMsg struct{ err error }

func (m *Model) currentLastView() domain.LastView {
	view := domain.LastView{Pane: "feeds", Scope: "all"}
	switch m.active {
	case articlesPane:
		view.Pane = "articles"
	case readerPane:
		view.Pane = "reader"
	}
	if m.filter.StarredOnly {
		view.Scope = "starred"
	} else if m.filter.FeedID != 0 {
		view.Scope, view.FeedID = "feed", m.filter.FeedID
	}
	for _, feed := range m.allFeeds {
		if feed.ID == view.FeedID {
			view.FeedURL = feed.URL
			break
		}
	}
	var entry domain.Entry
	if m.active == readerPane && m.readerEntry != nil {
		entry = *m.readerEntry
	} else if m.entryCursor >= 0 && m.entryCursor < len(m.entries) {
		entry = m.entries[m.entryCursor]
	}
	view.EntryID, view.EntryIdentity = entry.ID, entry.Identity
	for _, feed := range m.allFeeds {
		if feed.ID == entry.FeedID {
			view.EntryFeedURL = feed.URL
			break
		}
	}
	return view
}

func (m *Model) newLastViewTask() *lastViewTask {
	store, ok := m.store.(lastViewStore)
	if !ok {
		return nil
	}
	view := m.currentLastView()
	return &lastViewTask{run: func(ctx context.Context) error { return store.SaveLastView(ctx, view) }}
}

func (m *Model) queueLastView() tea.Cmd {
	if !m.resumeLastView || !m.hasLoaded {
		return nil
	}
	l := m.lifetime
	l.mu.Lock()
	l.lastView = m.newLastViewTask()
	l.mu.Unlock()
	if len(m.stateWrites) == 0 {
		return m.lastViewCmd()
	}
	return nil
}

func (m *Model) lastViewCmd() tea.Cmd {
	l := m.lifetime
	l.mu.Lock()
	task := l.lastView
	l.mu.Unlock()
	if task == nil {
		return tea.Quit
	}
	return func() tea.Msg {
		l.mu.Lock()
		if l.closed || l.lastView != task || task.started {
			l.mu.Unlock()
			return nil
		}
		task.started = true
		l.work.Add(1)
		l.mu.Unlock()
		defer l.work.Done()
		msg := lastViewMsg{err: task.run(l.ctx)}
		l.mu.Lock()
		task.result = &msg
		l.mu.Unlock()
		return msg
	}
}

func (m *Model) completeLastView(msg lastViewMsg) (tea.Model, tea.Cmd) {
	l := m.lifetime
	l.mu.Lock()
	l.lastView = nil
	l.mu.Unlock()
	if msg.err != nil {
		m.quitting = false
		m.lifetime.resumeBackground()
		m.closeOverlay()
		m.setError(msg.err)
		return m, nil
	}
	return m, tea.Quit
}
