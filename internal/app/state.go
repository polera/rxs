package app

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/polera/rxs/internal/domain"
)

type stateFields uint8

const (
	readField stateFields = 1 << iota
	starredField
	progressField
)

type entryState struct {
	read, starred bool
	progress      float64
}

type stateWrite struct {
	revision      uint64
	entryID       int64
	fields        stateFields
	before, after entryState
}

type stateMsg struct {
	write stateWrite
	saved entryState
	err   error
}

func stateOf(entry domain.Entry) entryState {
	return entryState{read: entry.Read, starred: entry.Starred, progress: entry.ReadingProgress}
}

func (s *entryState) copyFields(from entryState, fields stateFields) {
	if fields&readField != 0 {
		s.read = from.read
	}
	if fields&starredField != 0 {
		s.starred = from.starred
	}
	if fields&progressField != 0 {
		s.progress = from.progress
	}
}

func (m *Model) applyEntryState(id int64, state entryState, fields stateFields) {
	apply := func(entry *domain.Entry) {
		current := stateOf(*entry)
		current.copyFields(state, fields)
		entry.Read, entry.Starred, entry.ReadingProgress = current.read, current.starred, current.progress
	}
	for index := range m.entries {
		if m.entries[index].ID == id {
			apply(&m.entries[index])
		}
	}
	if m.readerEntry != nil && m.readerEntry.ID == id {
		apply(m.readerEntry)
	}
}

func (m *Model) queueStateWrite(entry domain.Entry, after entryState, fields stateFields) tea.Cmd {
	m.stateRevision++
	m.loadGeneration++ // A read already in flight may contain pre-mutation state.
	write := stateWrite{
		revision: m.stateRevision, entryID: entry.ID, fields: fields,
		before: stateOf(entry), after: after,
	}
	m.applyEntryState(entry.ID, after, fields)
	m.stateWrites = append(m.stateWrites, write)
	if len(m.stateWrites) == 1 {
		return m.stateWriteCmd(write)
	}
	return nil
}

func (m Model) stateWriteCmd(write stateWrite) tea.Cmd {
	store := m.store
	return func() tea.Msg {
		msg := stateMsg{write: write, saved: write.before}
		var errs []error
		if write.fields&progressField != 0 {
			if err := store.SetReadingProgress(context.Background(), write.entryID, write.after.progress); err != nil {
				errs = append(errs, err)
			} else {
				msg.saved.progress = write.after.progress
			}
		}
		if write.fields&readField != 0 {
			if err := store.SetRead(context.Background(), write.entryID, write.after.read); err != nil {
				errs = append(errs, err)
			} else {
				msg.saved.read = write.after.read
			}
		}
		if write.fields&starredField != 0 {
			if err := store.SetStarred(context.Background(), write.entryID, write.after.starred); err != nil {
				errs = append(errs, err)
			} else {
				msg.saved.starred = write.after.starred
			}
		}
		msg.err = errors.Join(errs...)
		return msg
	}
}

func (m Model) completeStateWrite(msg stateMsg) (tea.Model, tea.Cmd) {
	if len(m.stateWrites) == 0 || m.stateWrites[0].revision != msg.write.revision {
		return m, nil
	}
	m.stateWrites = m.stateWrites[1:]
	m.loadGeneration++
	remaining := msg.write.fields
	for index := range m.stateWrites {
		pending := &m.stateWrites[index]
		if pending.entryID != msg.write.entryID {
			continue
		}
		fields := remaining & pending.fields
		// A failed optimistic write must not become a later write's rollback value.
		pending.before.copyFields(msg.saved, fields)
		remaining &^= fields
	}
	m.applyEntryState(msg.write.entryID, msg.saved, remaining)
	if msg.err != nil {
		m.setError(msg.err)
		if m.quitting {
			m.quitting = false
			m.closeOverlay()
		}
	}
	if len(m.stateWrites) > 0 {
		return m, m.stateWriteCmd(m.stateWrites[0])
	}
	if m.quitting {
		return m, tea.Quit
	}
	return m, m.loadCmdPreserving()
}
