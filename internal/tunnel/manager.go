package tunnel

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	gossh "golang.org/x/crypto/ssh"
)

// Tunnel represents an active tunnel
type Tunnel struct {
	ID         string
	Subdomain  string
	SSHConn    *gossh.ServerConn
	BindAddr   string // address the client requested to bind (must match in forwarded-tcpip)
	BindPort   uint32 // port the client requested (usually 0)
	ClientPort uint32 // allocated port for forwarded-tcpip
	CreatedAt  time.Time
}

// Manager handles tunnel lifecycle
type Manager struct {
	mu         sync.RWMutex
	tunnels    map[string]*Tunnel // subdomain -> tunnel
	byID       map[string]*Tunnel // tunnelID -> tunnel
	baseDomain string
	nextPort   uint32 // port allocator for -R 0 requests
}

// NewManager creates a new tunnel manager
func NewManager(baseDomain string) *Manager {
	return &Manager{
		tunnels:    make(map[string]*Tunnel),
		byID:       make(map[string]*Tunnel),
		baseDomain: baseDomain,
		nextPort:   10000,
	}
}

// CreateTunnel creates a new tunnel for an SSH connection.
// Returns the tunnel, public URL, and allocated port.
func (m *Manager) CreateTunnel(sshConn *gossh.ServerConn, bindAddr string, requestedPort uint32) (*Tunnel, string, uint32) {
	subdomain := uuid.New().String()[:8]
	tunnelID := uuid.New().String()

	// Allocate a port if the client requested port 0
	allocatedPort := requestedPort
	if allocatedPort == 0 {
		m.mu.Lock()
		allocatedPort = m.nextPort
		m.nextPort++
		m.mu.Unlock()
	}

	tunnel := &Tunnel{
		ID:         tunnelID,
		Subdomain:  subdomain,
		SSHConn:    sshConn,
		BindAddr:   bindAddr,
		BindPort:   requestedPort,
		ClientPort: allocatedPort,
		CreatedAt:  time.Now(),
	}

	m.mu.Lock()
	m.tunnels[subdomain] = tunnel
	m.byID[tunnelID] = tunnel
	m.mu.Unlock()

	url := fmt.Sprintf("https://%s.%s", subdomain, m.baseDomain)
	return tunnel, url, allocatedPort
}

// GetBySubdomain looks up an active tunnel by subdomain
func (m *Manager) GetBySubdomain(subdomain string) (*Tunnel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tunnels[subdomain]
	return t, ok
}

// GetByID looks up an active tunnel by ID
func (m *Manager) GetByID(id string) (*Tunnel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.byID[id]
	return t, ok
}

// Close removes a tunnel
func (m *Manager) Close(tunnelID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tunnel, ok := m.byID[tunnelID]
	if !ok {
		return
	}

	delete(m.tunnels, tunnel.Subdomain)
	delete(m.byID, tunnelID)
}

// OpenChannel opens a forwarded-tcpip channel to the SSH client.
// This is how we send HTTP traffic back through the SSH connection.
func (m *Manager) OpenChannel(tunnel *Tunnel) (gossh.Channel, error) {
	// The payload for forwarded-tcpip channel
	// CRITICAL: Addr and Port must match what client sent in tcpip-forward
	// so the client can look up its forwarding rule
	payload := gossh.Marshal(struct {
		Addr       string
		Port       uint32
		OriginAddr string
		OriginPort uint32
	}{
		Addr:       tunnel.BindAddr,
		Port:       tunnel.ClientPort,
		OriginAddr: "127.0.0.1",
		OriginPort: 0,
	})

	ch, reqs, err := tunnel.SSHConn.OpenChannel("forwarded-tcpip", payload)
	if err != nil {
		return nil, err
	}

	// Discard any channel requests
	go gossh.DiscardRequests(reqs)

	return ch, nil
}
