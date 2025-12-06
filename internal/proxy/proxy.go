package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/MorrisonWill/duct.sh/internal/events"
	"github.com/MorrisonWill/duct.sh/internal/tunnel"
)

type Proxy struct {
	server        *http.Server
	tunnelManager *tunnel.Manager
	eventHub      *events.Hub
	baseDomain    string
	port          int
}

func New(
	tunnelManager *tunnel.Manager,
	eventHub *events.Hub,
	baseDomain string,
	port int,
) *Proxy {
	p := &Proxy{
		tunnelManager: tunnelManager,
		eventHub:      eventHub,
		baseDomain:    baseDomain,
		port:          port,
	}

	p.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: p,
	}

	return p
}

func (p *Proxy) Start() error {
	log.Printf("HTTP proxy listening on port %d", p.port)
	return p.server.ListenAndServe()
}

func (p *Proxy) Shutdown(ctx context.Context) error {
	return p.server.Shutdown(ctx)
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	subdomain := p.extractSubdomain(r.Host)
	if subdomain == "" {
		http.Error(w, "Invalid host", http.StatusBadRequest)
		return
	}

	tun, ok := p.tunnelManager.GetBySubdomain(subdomain)
	if !ok {
		http.Error(w, "Tunnel not found", http.StatusNotFound)
		return
	}

	// Open a channel through the SSH connection to the client
	ch, err := p.tunnelManager.OpenChannel(tun)
	if err != nil {
		log.Printf("Failed to open channel: %v", err)
		http.Error(w, "Tunnel unavailable", http.StatusBadGateway)
		return
	}
	defer ch.Close()

	// Write the HTTP request to the SSH channel
	if err := r.Write(ch); err != nil {
		log.Printf("Failed to write request: %v", err)
		http.Error(w, "Failed to forward request", http.StatusBadGateway)
		return
	}

	// Read the response from the SSH channel
	resp, err := http.ReadResponse(bufio.NewReader(ch), r)
	if err != nil {
		log.Printf("Failed to read response: %v", err)
		http.Error(w, "Failed to read response", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status and body
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)

	// Publish event
	duration := time.Since(startTime)
	p.eventHub.Publish(events.RequestEvent{
		TunnelID:   tun.ID,
		Subdomain:  tun.Subdomain,
		Timestamp:  startTime,
		Method:     r.Method,
		Path:       r.URL.Path,
		StatusCode: resp.StatusCode,
		DurationMs: duration.Milliseconds(),
		ClientIP:   p.extractClientIP(r),
	})
}

func (p *Proxy) extractSubdomain(host string) string {
	// Remove port if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	suffix := "." + p.baseDomain
	if !strings.HasSuffix(host, suffix) {
		return ""
	}

	subdomain := strings.TrimSuffix(host, suffix)
	if subdomain == "" || strings.Contains(subdomain, ".") {
		return ""
	}

	return subdomain
}

func (p *Proxy) extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
