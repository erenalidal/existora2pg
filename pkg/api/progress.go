package api

import (
	"sync"
)

// Broadcaster manages Server-Sent Event subscriptions and broadcasts progress events
// to all connected SSE clients for a given project.
type Broadcaster struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan ProgressEvent]struct{} // projectID -> set of channels
}

// NewBroadcaster creates a new SSE progress broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subscribers: make(map[string]map[chan ProgressEvent]struct{}),
	}
}

// Subscribe registers a new SSE client for a project.
// Returns a channel that receives progress events and an unsubscribe function.
func (b *Broadcaster) Subscribe(projectID string) (<-chan ProgressEvent, func()) {
	ch := make(chan ProgressEvent, 64)

	b.mu.Lock()
	if b.subscribers[projectID] == nil {
		b.subscribers[projectID] = make(map[chan ProgressEvent]struct{})
	}
	b.subscribers[projectID][ch] = struct{}{}
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		delete(b.subscribers[projectID], ch)
		if len(b.subscribers[projectID]) == 0 {
			delete(b.subscribers, projectID)
		}
		b.mu.Unlock()
		// Drain remaining buffered events (non-blocking).
		// Channel may or may not be closed by Close() — drain what's there.
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return // channel closed
				}
			default:
				return // no more buffered events
			}
		}
	}

	return ch, unsubscribe
}

// Send broadcasts a progress event to all subscribers of a project.
// Non-blocking: if a subscriber's channel is full, the event is dropped for that subscriber.
func (b *Broadcaster) Send(projectID string, event ProgressEvent) {
	b.mu.RLock()
	// Copy channel references under lock to avoid race with unsubscribe/close
	subs := b.subscribers[projectID]
	channels := make([]chan ProgressEvent, 0, len(subs))
	for ch := range subs {
		channels = append(channels, ch)
	}
	b.mu.RUnlock()

	for _, ch := range channels {
		select {
		case ch <- event:
		default:
			// Drop event if subscriber is too slow
		}
	}
}

// Close shuts down all subscribers for a project by closing their channels.
// This signals SSE handlers to terminate.
func (b *Broadcaster) Close(projectID string) {
	b.mu.Lock()
	subs := b.subscribers[projectID]
	delete(b.subscribers, projectID)
	b.mu.Unlock()

	for ch := range subs {
		close(ch)
	}
}

// HasSubscribers returns true if the project has active SSE listeners.
func (b *Broadcaster) HasSubscribers(projectID string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers[projectID]) > 0
}

// ProgressCallback returns a function suitable for use as a migration progress callback.
// It sends progress events to all SSE subscribers of the given project.
func (b *Broadcaster) ProgressCallback(projectID string) func(table, partition, state string, rows, speed int64, percent float64) {
	return func(table, partition, state string, rows, speed int64, percent float64) {
		b.Send(projectID, ProgressEvent{
			Table:     table,
			Partition: partition,
			Rows:      rows,
			Speed:     speed,
			Percent:   percent,
			State:     state,
		})
	}
}
