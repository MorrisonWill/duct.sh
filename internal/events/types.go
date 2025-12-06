package events

import (
	"net/http"
	"time"
)

// MaxBodySize is the maximum body size to capture (64KB)
const MaxBodySize = 64 * 1024

// CapturedRequest stores full request details for inspection and replay
type CapturedRequest struct {
	Method    string
	Path      string
	Query     string // Raw query string
	Proto     string // HTTP/1.1, etc.
	Host      string
	ClientIP  string
	Headers   http.Header
	Body      []byte
	BodySize  int64 // Original body size (before truncation)
	Truncated bool  // True if body was truncated
}

// CapturedResponse stores full response details for inspection
type CapturedResponse struct {
	StatusCode int
	Status     string // Full status text (e.g., "200 OK")
	Proto      string
	Headers    http.Header
	Body       []byte
	BodySize   int64
	Truncated  bool
}

// RequestRecord is a complete request/response pair with metadata
type RequestRecord struct {
	ID         string            // UUID for this record
	TunnelID   string            // Tunnel that handled the request
	Subdomain  string            // Public subdomain
	Timestamp  time.Time         // When the request was received
	DurationMs int64             // Round-trip duration in milliseconds
	Request    CapturedRequest   // Full request data
	Response   *CapturedResponse // Full response data (nil if request failed)
	Error      string            // Non-empty if request failed before response
}
