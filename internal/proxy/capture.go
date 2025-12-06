package proxy

import (
	"bytes"
	"io"
	"net/http"

	"github.com/MorrisonWill/duct.sh/internal/events"
)

// captureRequest extracts request data for storage without consuming the body.
// Returns the captured request data and a new body that should replace r.Body.
func captureRequest(r *http.Request, clientIP string) (*events.CapturedRequest, io.ReadCloser) {
	captured := &events.CapturedRequest{
		Method:   r.Method,
		Path:     r.URL.Path,
		Query:    r.URL.RawQuery,
		Proto:    r.Proto,
		Host:     r.Host,
		ClientIP: clientIP,
		Headers:  r.Header.Clone(),
	}

	if r.Body == nil || r.Body == http.NoBody {
		return captured, r.Body
	}

	// Create a buffer to capture the body as it's read
	var buf bytes.Buffer
	limitedReader := io.LimitReader(r.Body, events.MaxBodySize+1) // +1 to detect truncation

	// TeeReader copies to buf as the body is read
	tee := io.TeeReader(limitedReader, &buf)

	newBody := &captureBody{
		Reader:   tee,
		original: r.Body,
		captured: captured,
		buffer:   &buf,
	}

	return captured, newBody
}

// captureBody wraps a request body to capture its contents as it's read
type captureBody struct {
	io.Reader
	original io.ReadCloser
	captured *events.CapturedRequest
	buffer   *bytes.Buffer
	closed   bool
}

func (c *captureBody) Read(p []byte) (n int, err error) {
	return c.Reader.Read(p)
}

func (c *captureBody) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true

	// Finalize captured body data
	c.finalizeCapture()

	return c.original.Close()
}

func (c *captureBody) finalizeCapture() {
	data := c.buffer.Bytes()
	if int64(len(data)) > events.MaxBodySize {
		c.captured.Body = data[:events.MaxBodySize]
		c.captured.Truncated = true
		c.captured.BodySize = int64(len(data))
	} else {
		c.captured.Body = data
		c.captured.BodySize = int64(len(data))
	}
}

// captureResponseWriter wraps an http.ResponseWriter to capture response data
// while streaming the full response to the client.
type captureResponseWriter struct {
	http.ResponseWriter
	captured    *events.CapturedResponse
	buffer      *bytes.Buffer
	totalSize   int64
	wroteHeader bool
}

func newCaptureResponseWriter(w http.ResponseWriter) *captureResponseWriter {
	return &captureResponseWriter{
		ResponseWriter: w,
		captured: &events.CapturedResponse{
			Headers: make(http.Header),
		},
		buffer: &bytes.Buffer{},
	}
}

func (c *captureResponseWriter) WriteHeader(statusCode int) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true
	c.captured.StatusCode = statusCode
	c.captured.Status = http.StatusText(statusCode)
	c.captured.Headers = c.Header().Clone()
	c.ResponseWriter.WriteHeader(statusCode)
}

func (c *captureResponseWriter) Write(data []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}

	// Capture up to MaxBodySize
	if c.buffer.Len() < int(events.MaxBodySize) {
		remaining := int(events.MaxBodySize) - c.buffer.Len()
		if len(data) <= remaining {
			c.buffer.Write(data)
		} else {
			c.buffer.Write(data[:remaining])
		}
	}

	c.totalSize += int64(len(data))

	// Always write full data to client
	return c.ResponseWriter.Write(data)
}

func (c *captureResponseWriter) finalize() *events.CapturedResponse {
	c.captured.Body = c.buffer.Bytes()
	c.captured.BodySize = c.totalSize
	c.captured.Truncated = c.totalSize > events.MaxBodySize
	return c.captured
}
