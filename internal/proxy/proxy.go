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

	"github.com/google/uuid"

	"github.com/MorrisonWill/duct.sh/internal/events"
	"github.com/MorrisonWill/duct.sh/internal/store"
	"github.com/MorrisonWill/duct.sh/internal/tunnel"
)

type Proxy struct {
	server        *http.Server
	tunnelManager *tunnel.Manager
	eventHub      *events.Hub
	store         *store.RequestStore
	baseDomain    string
	port          int
}

func New(
	tunnelManager *tunnel.Manager,
	eventHub *events.Hub,
	requestStore *store.RequestStore,
	baseDomain string,
	port int,
) *Proxy {
	p := &Proxy{
		tunnelManager: tunnelManager,
		eventHub:      eventHub,
		store:         requestStore,
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
	recordID := uuid.New().String()

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

	// Show interstitial for first-time browser visitors
	if p.shouldShowInterstitial(r) {
		p.serveInterstitial(w, r, subdomain)
		return
	}

	clientIP := p.extractClientIP(r)

	// Capture request data before forwarding
	capturedReq, newBody := captureRequest(r, clientIP)
	r.Body = newBody

	// Open a channel through the SSH connection to the client
	ch, err := p.tunnelManager.OpenChannel(tun)
	if err != nil {
		log.Printf("Failed to open channel: %v", err)
		// Finalize request capture if body wasn't fully read
		if cb, ok := r.Body.(*captureBody); ok {
			cb.finalizeCapture()
		}
		// Store failed request record
		p.storeRecord(recordID, tun.ID, tun.Subdomain, startTime, capturedReq, nil, "Local service unavailable")
		p.eventHub.Publish(events.ErrorEvent{
			TunnelID:  tun.ID,
			Timestamp: time.Now(),
			Method:    r.Method,
			Path:      r.URL.Path,
			Message:   "Local service unavailable",
		})
		http.Error(w, "Tunnel unavailable", http.StatusBadGateway)
		return
	}
	defer ch.Close()

	// Write the HTTP request to the SSH channel
	if err := r.Write(ch); err != nil {
		log.Printf("Failed to write request: %v", err)
		p.storeRecord(recordID, tun.ID, tun.Subdomain, startTime, capturedReq, nil, "Failed to forward request")
		p.eventHub.Publish(events.ErrorEvent{
			TunnelID:  tun.ID,
			Timestamp: time.Now(),
			Method:    r.Method,
			Path:      r.URL.Path,
			Message:   "Failed to forward request",
		})
		http.Error(w, "Failed to forward request", http.StatusBadGateway)
		return
	}

	// Read the response from the SSH channel
	resp, err := http.ReadResponse(bufio.NewReader(ch), r)
	if err != nil {
		log.Printf("Failed to read response: %v", err)
		p.storeRecord(recordID, tun.ID, tun.Subdomain, startTime, capturedReq, nil, "Failed to read response")
		p.eventHub.Publish(events.ErrorEvent{
			TunnelID:  tun.ID,
			Timestamp: time.Now(),
			Method:    r.Method,
			Path:      r.URL.Path,
			Message:   "Failed to read response",
		})
		http.Error(w, "Failed to read response", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Wrap ResponseWriter to capture response while streaming full data to client
	captureWriter := newCaptureResponseWriter(w)

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			captureWriter.Header().Add(key, value)
		}
	}

	// Write status and stream full body to client (capture writer limits what's stored)
	captureWriter.WriteHeader(resp.StatusCode)
	io.Copy(captureWriter, resp.Body)

	// Finalize captured response
	capturedResp := captureWriter.finalize()

	// Calculate duration and store the complete record
	duration := time.Since(startTime)
	p.storeRecord(recordID, tun.ID, tun.Subdomain, startTime, capturedReq, capturedResp, "")

	// Publish event with link to full record
	p.eventHub.Publish(events.RequestEvent{
		TunnelID:   tun.ID,
		Subdomain:  tun.Subdomain,
		Timestamp:  startTime,
		Method:     r.Method,
		Path:       r.URL.Path,
		StatusCode: resp.StatusCode,
		DurationMs: duration.Milliseconds(),
		ClientIP:   clientIP,
		RecordID:   recordID,
	})
}

// storeRecord saves a request record to the store
func (p *Proxy) storeRecord(
	id, tunnelID, subdomain string,
	timestamp time.Time,
	req *events.CapturedRequest,
	resp *events.CapturedResponse,
	errMsg string,
) {
	duration := time.Since(timestamp)
	record := &events.RequestRecord{
		ID:         id,
		TunnelID:   tunnelID,
		Subdomain:  subdomain,
		Timestamp:  timestamp,
		DurationMs: duration.Milliseconds(),
		Request:    *req,
		Response:   resp,
		Error:      errMsg,
	}
	p.store.Store(record)
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

const bypassCookieName = "duct_bypass"

// shouldShowInterstitial returns true if we should show the disclaimer page
func (p *Proxy) shouldShowInterstitial(r *http.Request) bool {
	// Only show for GET requests (initial page loads)
	if r.Method != http.MethodGet {
		return false
	}

	// Skip if already bypassed via cookie
	if _, err := r.Cookie(bypassCookieName); err == nil {
		return false
	}

	// Only show for requests that look like browsers wanting HTML
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

// serveInterstitial shows a disclaimer page that sets a bypass cookie
func (p *Proxy) serveInterstitial(w http.ResponseWriter, r *http.Request, subdomain string) {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>duct.sh - Tunnel Disclaimer</title>
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
            max-width: 500px;
        }
        h1 {
            font-size: 1.5rem;
            font-weight: 600;
            margin-bottom: 1.5rem;
            color: #fff;
        }
        .warning {
            background: #1a1a1a;
            border: 1px solid #333;
            border-radius: 8px;
            padding: 1.5rem;
            margin-bottom: 1.5rem;
            text-align: left;
        }
        .warning p {
            color: #999;
            line-height: 1.6;
            margin-bottom: 1rem;
        }
        .warning p:last-child {
            margin-bottom: 0;
        }
        .subdomain {
            font-family: 'SF Mono', Monaco, 'Courier New', monospace;
            color: #4ade80;
        }
        .button {
            display: inline-block;
            background: #fff;
            color: #0a0a0a;
            padding: 0.75rem 2rem;
            border-radius: 6px;
            text-decoration: none;
            font-weight: 500;
            font-size: 1rem;
            cursor: pointer;
            border: none;
        }
        .button:hover {
            background: #e5e5e5;
        }
        .footer {
            margin-top: 2rem;
            font-size: 0.875rem;
            color: #555;
        }
        .footer a {
            color: #888;
            text-decoration: none;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>You're visiting a tunneled site</h1>
        <div class="warning">
            <p>This site is served through <span class="subdomain">` + subdomain + `.duct.sh</span>,
            a temporary tunnel created by a duct.sh user.</p>
            <p>The content comes from someone's local machine and is not hosted by duct.sh.
            Proceed only if you trust the source.</p>
        </div>
        <form method="GET">
            <button type="submit" class="button" name="duct_continue" value="1">Continue to site</button>
        </form>
        <div class="footer">Powered by <a href="https://duct.sh">duct.sh</a></div>
    </div>
</body>
</html>`

	// If user clicked continue, set cookie and redirect
	if r.URL.Query().Get("duct_continue") == "1" {
		http.SetCookie(w, &http.Cookie{
			Name:     bypassCookieName,
			Value:    "1",
			Path:     "/",
			MaxAge:   86400 * 7, // 7 days
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		// Redirect to clean URL (remove the query param)
		cleanURL := *r.URL
		q := cleanURL.Query()
		q.Del("duct_continue")
		cleanURL.RawQuery = q.Encode()
		http.Redirect(w, r, cleanURL.String(), http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}

func (p *Proxy) serveLandingPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/robots.txt" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("User-agent: *\nAllow: /\n"))
		return
	}

	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <meta name="description" content="Expose local servers to the internet via SSH.">
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
            max-width: 600px;
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
            margin-bottom: 2.5rem;
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
        .features {
            margin-top: 3rem;
            display: grid;
            grid-template-columns: 1fr 1fr;
            gap: 1.5rem;
            text-align: left;
        }
        .feature {
            padding: 1rem;
        }
        .feature-title {
            font-size: 1rem;
            font-weight: 600;
            color: #fff;
            margin-bottom: 0.5rem;
        }
        .feature-desc {
            font-size: 0.9375rem;
            color: #666;
            line-height: 1.5;
        }
        .footer {
            margin-top: 3rem;
            font-size: 0.875rem;
            color: #555;
        }
        .footer a {
            color: #555;
            text-decoration: none;
        }
        .footer a:hover {
            color: #888;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>duct.sh</h1>
        <p class="tagline">Expose local servers to the internet via SSH.</p>
        <div class="code">ssh -R 0:localhost:3000 duct.sh</div>
        <div class="features">
            <div class="feature">
                <div class="feature-title">Live request log</div>
                <div class="feature-desc">Watch requests stream in real-time from your terminal.</div>
            </div>
            <div class="feature">
                <div class="feature-title">Inspect traffic</div>
                <div class="feature-desc">View full request and response headers and bodies.</div>
            </div>
            <div class="feature">
                <div class="feature-title">Replay requests</div>
                <div class="feature-desc">Re-send any captured request with a single keystroke.</div>
            </div>
            <div class="feature">
                <div class="feature-title">Export to curl</div>
                <div class="feature-desc">Copy any request as a curl command to your clipboard.</div>
            </div>
        </div>
        <div class="footer"><a href="https://github.com/MorrisonWill/duct.sh">GitHub</a></div>
    </div>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}
