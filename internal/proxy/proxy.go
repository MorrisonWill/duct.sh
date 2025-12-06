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
		p.serveLandingPage(w, r)
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

func (p *Proxy) serveLandingPage(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>duct.sh</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: system-ui, -apple-system, sans-serif;
            background: #0a0a0a;
            color: #e5e5e5;
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .container {
            text-align: center;
            padding: 2rem;
        }
        h1 {
            font-size: 3rem;
            font-weight: 300;
            margin-bottom: 1rem;
            color: #fff;
        }
        .tagline {
            font-size: 1.25rem;
            color: #888;
            margin-bottom: 3rem;
        }
        .code {
            background: #1a1a1a;
            border: 1px solid #333;
            border-radius: 8px;
            padding: 1.5rem 2rem;
            font-family: 'SF Mono', Monaco, 'Courier New', monospace;
            font-size: 1rem;
            color: #4ade80;
            display: inline-block;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>duct.sh</h1>
        <p class="tagline">Expose local servers to the internet via SSH.</p>
        <div class="code">ssh -R 0:localhost:3000 duct.sh</div>
    </div>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}
