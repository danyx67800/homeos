// Package hub fans one telemetry sample out to every connected dashboard,
// over WebSocket or SSE. Both transports subscribe the same way.
package hub

import (
	"sync"
	"sync/atomic"
)

// Event is what crosses the wire. Type lets the dashboard demultiplex without
// inspecting the payload.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Subscriber receives events on C until Close is called. C is buffered; a
// subscriber that stops reading is dropped rather than allowed to block the
// publisher, because one stalled browser tab must not stop telemetry for
// everyone else.
type Subscriber struct {
	C      chan Event
	hub    *Hub
	id     uint64
	closed atomic.Bool
}

type Hub struct {
	mu      sync.RWMutex
	subs    map[uint64]*Subscriber
	nextID  uint64
	bufSize int

	// Last event of each type, replayed to a new subscriber so a freshly
	// opened dashboard renders immediately instead of waiting for the next tick.
	lastMu sync.RWMutex
	last   map[string]Event

	dropped atomic.Uint64
}

func New(bufSize int) *Hub {
	if bufSize <= 0 {
		bufSize = 16
	}
	return &Hub{
		subs:    make(map[uint64]*Subscriber),
		last:    make(map[string]Event),
		bufSize: bufSize,
	}
}

func (h *Hub) Subscribe() *Subscriber {
	h.mu.Lock()
	h.nextID++
	s := &Subscriber{C: make(chan Event, h.bufSize), hub: h, id: h.nextID}
	h.subs[s.id] = s
	h.mu.Unlock()

	// Replay the latest of each type. Done after registration so a concurrent
	// Publish is not lost, and non-blocking so a small buffer cannot deadlock.
	h.lastMu.RLock()
	for _, ev := range h.last {
		select {
		case s.C <- ev:
		default:
		}
	}
	h.lastMu.RUnlock()

	return s
}

// Close is idempotent: an HTTP handler may unsubscribe from both its read loop
// and its deferred cleanup.
func (s *Subscriber) Close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	s.hub.mu.Lock()
	delete(s.hub.subs, s.id)
	s.hub.mu.Unlock()
	close(s.C)
}

// Publish never blocks. A subscriber whose buffer is full misses this event and
// is counted; telemetry is a stream of samples, so dropping a stale one is
// strictly better than stalling the collector.
func (h *Hub) Publish(ev Event) {
	h.lastMu.Lock()
	h.last[ev.Type] = ev
	h.lastMu.Unlock()

	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.subs {
		select {
		case s.C <- ev:
		default:
			h.dropped.Add(1)
		}
	}
}

func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

func (h *Hub) Dropped() uint64 { return h.dropped.Load() }

// Last returns the most recent event of a type, for the plain REST endpoints
// that answer with a snapshot rather than a stream.
func (h *Hub) Last(typ string) (Event, bool) {
	h.lastMu.RLock()
	defer h.lastMu.RUnlock()
	ev, ok := h.last[typ]
	return ev, ok
}
