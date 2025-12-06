package replay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/MorrisonWill/duct.sh/internal/events"
)

// Replayer handles replaying captured requests through the HTTP proxy
type Replayer struct {
	client *http.Client
}

// New creates a new Replayer
func New() *Replayer {
	return &Replayer{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Replay sends a captured request through the proxy (via public URL) and returns the response status.
// This ensures replayed requests are captured and appear in the request list.
func (r *Replayer) Replay(tunnelURL string, captured *events.CapturedRequest) (statusCode int, durationMs int64, err error) {
	start := time.Now()

	// Parse the tunnel URL to extract host and construct local request
	parsedURL, err := url.Parse(tunnelURL)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse tunnel URL: %w", err)
	}

	// Build the request path
	path := captured.Path
	if captured.Query != "" {
		path += "?" + captured.Query
	}

	// Connect to localhost but use the subdomain host header for routing
	// This handles local development where subdomain.localhost doesn't resolve
	localURL := fmt.Sprintf("%s://127.0.0.1:%s%s", parsedURL.Scheme, parsedURL.Port(), path)
	if parsedURL.Port() == "" {
		if parsedURL.Scheme == "https" {
			localURL = fmt.Sprintf("%s://127.0.0.1:443%s", parsedURL.Scheme, path)
		} else {
			localURL = fmt.Sprintf("%s://127.0.0.1:80%s", parsedURL.Scheme, path)
		}
	}

	var body io.Reader
	if len(captured.Body) > 0 {
		body = bytes.NewReader(captured.Body)
	}

	req, err := http.NewRequest(captured.Method, localURL, body)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create request: %w", err)
	}

	// Set Host header to the tunnel's subdomain host so proxy can route it
	req.Host = parsedURL.Host

	// Restore headers (skip Host, we set it explicitly above)
	for key, values := range captured.Headers {
		if key == "Host" {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Set Content-Length if we have a body
	if len(captured.Body) > 0 {
		req.ContentLength = int64(len(captured.Body))
	}

	// Make the request through the proxy
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Drain response body
	io.Copy(io.Discard, resp.Body)

	durationMs = time.Since(start).Milliseconds()
	return resp.StatusCode, durationMs, nil
}
