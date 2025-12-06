package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"github.com/MorrisonWill/duct.sh/internal/events"
)

// ToCurlCommand converts a captured request to a curl command string
func ToCurlCommand(req *events.CapturedRequest, baseURL string) string {
	var parts []string

	parts = append(parts, "curl")

	// Method (only add -X if not GET)
	if req.Method != "GET" {
		parts = append(parts, "-X", req.Method)
	}

	// URL
	url := baseURL + req.Path
	if req.Query != "" {
		url += "?" + req.Query
	}
	parts = append(parts, shellQuote(url))

	// Headers (sorted for consistency)
	headerNames := make([]string, 0, len(req.Headers))
	for name := range req.Headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)

	for _, name := range headerNames {
		// Skip headers that curl sets automatically
		lower := strings.ToLower(name)
		if lower == "host" || lower == "content-length" || lower == "user-agent" {
			continue
		}

		for _, value := range req.Headers[name] {
			parts = append(parts, "-H", shellQuote(fmt.Sprintf("%s: %s", name, value)))
		}
	}

	// Body
	if len(req.Body) > 0 {
		// For binary or very long bodies, just indicate there's data
		if req.Truncated || !isPrintable(req.Body) {
			parts = append(parts, "-d", "'[body data]'")
		} else {
			parts = append(parts, "-d", shellQuote(string(req.Body)))
		}
	}

	return strings.Join(parts, " \\\n  ")
}

// shellQuote quotes a string for safe use in shell commands
func shellQuote(s string) string {
	// Use single quotes, escaping any existing single quotes
	escaped := strings.ReplaceAll(s, "'", "'\"'\"'")
	return "'" + escaped + "'"
}

// isPrintable checks if the body content is printable text
func isPrintable(data []byte) bool {
	for _, b := range data {
		if b < 32 && b != '\n' && b != '\r' && b != '\t' {
			return false
		}
		if b > 126 {
			return false
		}
	}
	return true
}

// copyToClipboard copies text to the system clipboard
func copyToClipboard(text string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		// Try xclip first, then xsel
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			return fmt.Errorf("no clipboard utility found (install xclip or xsel)")
		}
	case "windows":
		cmd = exec.Command("clip")
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	if _, err := stdin.Write([]byte(text)); err != nil {
		return err
	}

	if err := stdin.Close(); err != nil {
		return err
	}

	return cmd.Wait()
}
