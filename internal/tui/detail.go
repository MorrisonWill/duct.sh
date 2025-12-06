package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MorrisonWill/duct.sh/internal/events"
)

// renderDetail renders the current tab content (request or response)
func (m *TunnelModel) renderDetail() string {
	if m.detailRecord == nil {
		return m.styles.Dim.Render("No request selected")
	}

	r := m.detailRecord

	switch m.detailTab {
	case TabRequest:
		return m.renderRequestSection(&r.Request)
	case TabResponse:
		if r.Response != nil {
			return m.renderResponseSection(r.Response, r.DurationMs)
		} else if r.Error != "" {
			return m.styles.Error.Render(fmt.Sprintf("ERROR: %s", r.Error))
		}
		return m.styles.Dim.Render("No response (request failed)")
	}

	return ""
}

func (m *TunnelModel) renderRequestSection(req *events.CapturedRequest) string {
	var lines []string

	// Request line
	path := req.Path
	if req.Query != "" {
		path += "?" + req.Query
	}
	lines = append(lines, fmt.Sprintf("%s %s %s", req.Method, path, req.Proto))
	lines = append(lines, fmt.Sprintf("Host: %s", req.Host))

	// Headers
	lines = append(lines, m.formatHeaders(req.Headers)...)

	// Body
	if len(req.Body) > 0 {
		lines = append(lines, "")
		lines = append(lines, m.styles.Header.Render("Body"))
		lines = append(lines, m.formatBody(req.Body, req.Truncated, req.BodySize)...)
	}

	return strings.Join(lines, "\n")
}

func (m *TunnelModel) renderResponseSection(resp *events.CapturedResponse, durationMs int64) string {
	var lines []string

	// Status line
	lines = append(lines, fmt.Sprintf("%s %s", resp.Proto, resp.Status))

	// Headers
	lines = append(lines, m.formatHeaders(resp.Headers)...)

	// Body
	if len(resp.Body) > 0 {
		lines = append(lines, "")
		lines = append(lines, m.styles.Header.Render("Body"))
		lines = append(lines, m.formatBody(resp.Body, resp.Truncated, resp.BodySize)...)
	}

	return strings.Join(lines, "\n")
}

func (m *TunnelModel) formatHeaders(headers map[string][]string) []string {
	var lines []string

	// Sort header names for consistent display
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		values := headers[name]
		for _, value := range values {
			// Truncate long header values
			displayValue := value
			if len(displayValue) > 80 {
				displayValue = displayValue[:77] + "..."
			}
			lines = append(lines, fmt.Sprintf("%s: %s", name, displayValue))
		}
	}

	return lines
}

func (m *TunnelModel) formatBody(body []byte, truncated bool, originalSize int64) []string {
	var lines []string

	content := string(body)

	// Split and wrap long lines
	bodyLines := strings.Split(content, "\n")
	for _, line := range bodyLines {
		if len(line) > 120 {
			for len(line) > 120 {
				lines = append(lines, line[:120])
				line = line[120:]
			}
			if len(line) > 0 {
				lines = append(lines, line)
			}
		} else {
			lines = append(lines, line)
		}
	}

	// Add truncation notice if applicable
	if truncated {
		lines = append(lines, "")
		notice := fmt.Sprintf("[Body truncated: %d bytes shown of %d total]",
			len(body), originalSize)
		lines = append(lines, m.styles.Dim.Render(notice))
	}

	return lines
}
