package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MorrisonWill/duct.sh/internal/events"
	"github.com/MorrisonWill/duct.sh/internal/tunnel"
)

// Key bindings
var quitKeys = key.NewBinding(
	key.WithKeys("q", "ctrl+c"),
	key.WithHelp("q", "quit"),
)

// channelClosedMsg is sent when the event channel is closed
type channelClosedMsg struct{}

// Styles holds all TUI styles (created per-session for correct color profiles)
type Styles struct {
	Title   lipgloss.Style
	URL     lipgloss.Style
	Dim     lipgloss.Style
	Method  lipgloss.Style
	Success lipgloss.Style
	Warn    lipgloss.Style
	Error   lipgloss.Style
}

// NewStyles creates styles using the provided renderer for correct color detection
func NewStyles(renderer *lipgloss.Renderer) Styles {
	return Styles{
		Title: renderer.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212")),
		URL: renderer.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86")).
			Background(lipgloss.Color("236")).
			Padding(0, 2),
		Dim: renderer.NewStyle().
			Foreground(lipgloss.Color("241")),
		Method: renderer.NewStyle().
			Width(7),
		Success: renderer.NewStyle().
			Foreground(lipgloss.Color("46")),
		Warn: renderer.NewStyle().
			Foreground(lipgloss.Color("226")),
		Error: renderer.NewStyle().
			Foreground(lipgloss.Color("196")),
	}
}

// TunnelModel is the main TUI for an active tunnel
type TunnelModel struct {
	tunnel       *tunnel.Tunnel
	tunnelURL    string
	eventCh      <-chan events.RequestEvent
	cleanup      func()
	requests     []events.RequestEvent
	requestCount int
	startTime    time.Time
	width        int
	height       int
	quitting     bool
	styles       Styles
}

func NewTunnelModel(
	tun *tunnel.Tunnel,
	tunnelURL string,
	eventCh <-chan events.RequestEvent,
	cleanup func(),
	styles Styles,
) *TunnelModel {
	return &TunnelModel{
		tunnel:    tun,
		tunnelURL: tunnelURL,
		eventCh:   eventCh,
		cleanup:   cleanup,
		requests:  make([]events.RequestEvent, 0, 50),
		startTime: time.Now(),
		styles:    styles,
	}
}

func (m *TunnelModel) Init() tea.Cmd {
	return m.waitForEvent()
}

func (m *TunnelModel) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.eventCh
		if !ok {
			return channelClosedMsg{}
		}
		return event
	}
}

func (m *TunnelModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, quitKeys):
			m.quitting = true
			m.cleanup()
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case channelClosedMsg:
		m.quitting = true
		return m, tea.Quit

	case events.RequestEvent:
		m.requestCount++
		m.requests = append([]events.RequestEvent{msg}, m.requests...)
		if len(m.requests) > 50 {
			m.requests = m.requests[:50]
		}
		return m, m.waitForEvent()
	}

	return m, nil
}

func (m *TunnelModel) View() string {
	if m.quitting {
		return ""
	}

	title := m.styles.Title.Render("Duct")
	url := m.styles.URL.Render(m.tunnelURL)

	uptime := time.Since(m.startTime).Truncate(time.Second)
	stats := m.styles.Dim.Render(fmt.Sprintf(
		"Requests: %d | Uptime: %s",
		m.requestCount,
		uptime,
	))

	header := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		url,
		"",
		stats,
		"",
	)

	var requestsView string
	if len(m.requests) == 0 {
		requestsView = m.styles.Dim.Italic(true).Render("Waiting for requests...")
	} else {
		lines := make([]string, 0, len(m.requests))
		for _, r := range m.requests {
			line := m.formatRequest(r)
			lines = append(lines, line)
		}
		requestsView = lipgloss.JoinVertical(lipgloss.Left, lines...)
	}

	footer := m.styles.Dim.Render("Press q to quit")

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		requestsView,
		"",
		footer,
	)
}

func (m *TunnelModel) formatRequest(r events.RequestEvent) string {
	timeStr := r.Timestamp.Format("15:04:05")

	var statusStyle lipgloss.Style
	if r.StatusCode >= 400 {
		statusStyle = m.styles.Error
	} else if r.StatusCode >= 300 {
		statusStyle = m.styles.Warn
	} else {
		statusStyle = m.styles.Success
	}

	return fmt.Sprintf("%s %s %s %s %dms",
		m.styles.Dim.Render(timeStr),
		m.styles.Method.Render(r.Method),
		truncate(r.Path, 40),
		statusStyle.Render(fmt.Sprintf("%d", r.StatusCode)),
		r.DurationMs,
	)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// UsageModel is shown when user connects without -R
type UsageModel struct {
	baseDomain string
}

func NewUsageModel(baseDomain string) *UsageModel {
	return &UsageModel{baseDomain: baseDomain}
}

func (m *UsageModel) Init() tea.Cmd {
	return tea.Quit
}

func (m *UsageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, tea.Quit
}

func (m *UsageModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Render("Duct")

	usage := fmt.Sprintf(`
To create a tunnel, use the -R flag:

    ssh -R 0:localhost:3000 %s

This will expose your local port 3000 via a public URL.
`, m.baseDomain)

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		usage,
	)
}
