package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// lifecycle is shared by Tea's Model copies. Admission and shutdown use the
// same lock: a command either joins work before Wait, or never touches the store.
type lifecycle struct {
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	background  context.Context
	stop        context.CancelFunc
	loadCancel  context.CancelFunc
	closed      bool
	work        sync.WaitGroup
	pending     []*stateTask
	shutdown    sync.Once
	shutdownErr error
}

type stateTask struct {
	run      func(context.Context) stateMsg
	started  bool
	result   *stateMsg
	revision uint64
}

func newLifecycle() *lifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	background, stop := context.WithCancel(ctx)
	return &lifecycle{ctx: ctx, cancel: cancel, background: background, stop: stop}
}

func (l *lifecycle) backgroundContext(load bool) context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !load {
		return l.background
	}
	if l.loadCancel != nil {
		l.loadCancel()
	}
	ctx, cancel := context.WithCancel(l.background)
	l.loadCancel = cancel
	return ctx
}

func (l *lifecycle) cancelLoad() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loadCancel != nil {
		l.loadCancel()
	}
}

func (l *lifecycle) cancelBackground() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stop()
}

func (l *lifecycle) resumeBackground() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.background, l.stop = context.WithCancel(l.ctx)
	}
}

func (l *lifecycle) command(ctx context.Context, run func(context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return nil
		}
		l.work.Add(1)
		l.mu.Unlock()
		defer l.work.Done()
		// run returns the operation's typed cancellation message even when its
		// context was canceled before execution (notably the startup load).
		return run(ctx)
	}
}

func (l *lifecycle) acknowledgeState(revision uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.pending) > 0 && l.pending[0].revision == revision {
		l.pending = l.pending[1:]
	}
}

// Shutdown must be called after Program.Run returns and before closing Store,
// even when Run fails. It works on the original Model as well as the final copy.
// It rejects deferred commands, cancels background work, and flushes only intents
// already queued by Update, in order. Unsampled reader scroll is not an intent.
// ctx bounds the final flush, including active state writes. On expiry we cancel
// work but still join it: dependencies must honor context for prompt shutdown;
// returning early and racing Store.Close is never safe. Callers must not run
// Update concurrently with Shutdown. Repeated calls return the first result.
func (m Model) Shutdown(ctx context.Context) error {
	l := m.lifetime
	l.shutdown.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.stop()
		l.mu.Unlock()
		stop := context.AfterFunc(ctx, l.cancel)
		defer stop()
		defer l.cancel()
		if ctx.Err() != nil {
			l.cancel()
		}
		l.work.Wait()
		var errs []error
		for _, task := range l.pending {
			if task.result == nil {
				if err := ctx.Err(); err != nil {
					errs = append(errs, fmt.Errorf("flush queued state: %w", err))
					break
				}
				result := task.run(l.ctx)
				task.result = &result
			}
			if task.result.err != nil {
				errs = append(errs, fmt.Errorf("save queued state: %w", task.result.err))
			}
		}
		l.pending = nil
		l.shutdownErr = errors.Join(errs...)
	})
	return l.shutdownErr
}
