package events

import (
	"sync"
	"time"
)

// RequestEvent represents a proxied HTTP request
type RequestEvent struct {
	TunnelID   string
	Subdomain  string
	Timestamp  time.Time
	Method     string
	Path       string
	StatusCode int
	DurationMs int64
	ClientIP   string
}

// Hub manages event subscriptions and publishing
type Hub struct {
	mu          sync.RWMutex
	subscribers map[string]chan RequestEvent // tunnelID -> channel
	bufferSize  int
}

// NewHub creates a new event hub
func NewHub(bufferSize int) *Hub {
	return &Hub{
		subscribers: make(map[string]chan RequestEvent),
		bufferSize:  bufferSize,
	}
}

// Subscribe creates a subscription for a tunnel.
// Returns a channel that receives events for that tunnel.
// Only one subscriber per tunnel (the TUI session that owns it).
func (h *Hub) Subscribe(tunnelID string) <-chan RequestEvent {
	ch := make(chan RequestEvent, h.bufferSize)

	h.mu.Lock()
	h.subscribers[tunnelID] = ch
	h.mu.Unlock()

	return ch
}

// Unsubscribe removes the subscription for a tunnel
func (h *Hub) Unsubscribe(tunnelID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ch, ok := h.subscribers[tunnelID]; ok {
		close(ch)
		delete(h.subscribers, tunnelID)
	}
}

// Publish sends an event to the tunnel's subscriber.
// Non-blocking: drops event if channel is full.
func (h *Hub) Publish(event RequestEvent) {
	h.mu.RLock()
	ch, ok := h.subscribers[event.TunnelID]
	h.mu.RUnlock()

	if !ok {
		return
	}

	select {
	case ch <- event:
	default:
		// channel full, drop event
	}
}
