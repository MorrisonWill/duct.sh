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
	RecordID   string // Links to full RequestRecord in store
}

// ErrorEvent represents a forwarding error (e.g., local service unavailable)
type ErrorEvent struct {
	TunnelID  string
	Timestamp time.Time
	Method    string
	Path      string
	Message   string
}

// Event is an interface for tunnel events
type Event interface {
	GetTunnelID() string
}

func (e RequestEvent) GetTunnelID() string { return e.TunnelID }
func (e ErrorEvent) GetTunnelID() string   { return e.TunnelID }

// Hub manages event subscriptions and publishing
type Hub struct {
	mu          sync.RWMutex
	subscribers map[string]chan Event // tunnelID -> channel
	bufferSize  int
}

// NewHub creates a new event hub
func NewHub(bufferSize int) *Hub {
	return &Hub{
		subscribers: make(map[string]chan Event),
		bufferSize:  bufferSize,
	}
}

// Subscribe creates a subscription for a tunnel.
// Returns a channel that receives events for that tunnel.
// Only one subscriber per tunnel (the TUI session that owns it).
func (h *Hub) Subscribe(tunnelID string) <-chan Event {
	ch := make(chan Event, h.bufferSize)

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
func (h *Hub) Publish(event Event) {
	h.mu.RLock()
	ch, ok := h.subscribers[event.GetTunnelID()]
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
