package store

import (
	"sync"

	"github.com/MorrisonWill/duct.sh/internal/events"
)

// DefaultMaxRecords is the default number of records to keep per tunnel
const DefaultMaxRecords = 100

// RequestStore manages request records for all tunnels
type RequestStore struct {
	mu           sync.RWMutex
	tunnels      map[string]*TunnelStore
	maxPerTunnel int
}

// NewRequestStore creates a new request store
func NewRequestStore(maxPerTunnel int) *RequestStore {
	if maxPerTunnel <= 0 {
		maxPerTunnel = DefaultMaxRecords
	}
	return &RequestStore{
		tunnels:      make(map[string]*TunnelStore),
		maxPerTunnel: maxPerTunnel,
	}
}

// Store adds a request record to the store
func (s *RequestStore) Store(record *events.RequestRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ts, ok := s.tunnels[record.TunnelID]
	if !ok {
		ts = newTunnelStore(s.maxPerTunnel)
		s.tunnels[record.TunnelID] = ts
	}

	ts.add(record)
}

// Get retrieves a specific record by tunnel and record ID
func (s *RequestStore) Get(tunnelID, recordID string) (*events.RequestRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ts, ok := s.tunnels[tunnelID]
	if !ok {
		return nil, false
	}

	return ts.get(recordID)
}

// List returns all records for a tunnel in reverse chronological order (newest first)
func (s *RequestStore) List(tunnelID string) []*events.RequestRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ts, ok := s.tunnels[tunnelID]
	if !ok {
		return nil
	}

	return ts.list()
}

// Clear removes all records for a tunnel (call when tunnel closes)
func (s *RequestStore) Clear(tunnelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tunnels, tunnelID)
}

// TunnelStore holds records for a single tunnel using a ring buffer
type TunnelStore struct {
	records []*events.RequestRecord
	byID    map[string]*events.RequestRecord
	head    int // Next write position
	count   int // Current number of records
	max     int // Maximum records to keep
}

func newTunnelStore(max int) *TunnelStore {
	return &TunnelStore{
		records: make([]*events.RequestRecord, max),
		byID:    make(map[string]*events.RequestRecord),
		max:     max,
	}
}

func (ts *TunnelStore) add(record *events.RequestRecord) {
	// If overwriting, remove old record from map
	if ts.records[ts.head] != nil {
		delete(ts.byID, ts.records[ts.head].ID)
	}

	ts.records[ts.head] = record
	ts.byID[record.ID] = record

	ts.head = (ts.head + 1) % ts.max
	if ts.count < ts.max {
		ts.count++
	}
}

func (ts *TunnelStore) get(id string) (*events.RequestRecord, bool) {
	r, ok := ts.byID[id]
	return r, ok
}

func (ts *TunnelStore) list() []*events.RequestRecord {
	result := make([]*events.RequestRecord, 0, ts.count)

	// Return in reverse chronological order (newest first)
	for i := 0; i < ts.count; i++ {
		idx := (ts.head - 1 - i + ts.max) % ts.max
		if ts.records[idx] != nil {
			result = append(result, ts.records[idx])
		}
	}

	return result
}
