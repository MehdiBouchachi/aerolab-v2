// Package sse is a small fan-out hub for Server-Sent Events. Every subscriber
// has a bounded queue; a subscriber that cannot keep up is closed (the client
// reconnects and re-fetches a fresh snapshot) instead of growing Pi memory.
package sse

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
)

const (
	QueueLen       = 64
	MaxSubscribers = 32
	MaxPerUser     = 5
)

var ErrTooMany = errors.New("too many event streams")

type Event struct {
	ID   uint64 `json:"id"`
	Type string `json:"type"`
	Data []byte `json:"data"` // pre-marshalled JSON
}

type Sub struct {
	C      chan Event
	userID string
	once   sync.Once
	hub    *Hub
}

type Hub struct {
	mu      sync.Mutex
	subs    map[*Sub]struct{}
	perUser map[string]int
	seq     atomic.Uint64
	dropped atomic.Int64
}

func NewHub() *Hub { return &Hub{subs: map[*Sub]struct{}{}, perUser: map[string]int{}} }

func (h *Hub) Subscribe(userID string) (*Sub, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= MaxSubscribers || h.perUser[userID] >= MaxPerUser {
		return nil, ErrTooMany
	}
	s := &Sub{C: make(chan Event, QueueLen), userID: userID, hub: h}
	h.subs[s] = struct{}{}
	h.perUser[userID]++
	return s, nil
}

func (s *Sub) Close() {
	s.once.Do(func() {
		h := s.hub
		h.mu.Lock()
		delete(h.subs, s)
		if h.perUser[s.userID]--; h.perUser[s.userID] <= 0 {
			delete(h.perUser, s.userID)
		}
		h.mu.Unlock()
		close(s.C)
	})
}

// Publish marshals v once and offers it to every subscriber without blocking.
func (h *Hub) Publish(typ string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	ev := Event{ID: h.seq.Add(1), Type: typ, Data: b}
	h.mu.Lock()
	var slow []*Sub
	for s := range h.subs {
		select {
		case s.C <- ev:
		default:
			slow = append(slow, s)
		}
	}
	h.mu.Unlock()
	for _, s := range slow {
		h.dropped.Add(1)
		s.Close()
	}
}

func (h *Hub) Count() int     { h.mu.Lock(); defer h.mu.Unlock(); return len(h.subs) }
func (h *Hub) Dropped() int64 { return h.dropped.Load() }
func (h *Hub) LastID() uint64 { return h.seq.Load() }
