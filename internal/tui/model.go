package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MorrisonWill/duct.sh/internal/events"
	"github.com/MorrisonWill/duct.sh/internal/store"
	"github.com/MorrisonWill/duct.sh/internal/tunnel"
)

// ViewMode represents the current view state
type ViewMode int

const (
	ViewList   ViewMode = iota // Request list view
	ViewDetail                 // Full request/response detail view
)

// DetailTab represents which tab is active in detail view
type DetailTab int

const (
	TabRequest  DetailTab = iota // Request tab
	TabResponse                  // Response tab
)

// Key bindings
var quitKeys = key.NewBinding(
	key.WithKeys("q", "ctrl+c"),
	key.WithHelp("q", "quit"),
)

var redrawKeys = key.NewBinding(
	key.WithKeys("ctrl+l"),
	key.WithHelp("ctrl+l", "redraw"),
)

var upKeys = key.NewBinding(
	key.WithKeys("up", "k"),
	key.WithHelp("↑/k", "up"),
)

var downKeys = key.NewBinding(
	key.WithKeys("down", "j"),
	key.WithHelp("↓/j", "down"),
)

var enterKeys = key.NewBinding(
	key.WithKeys("enter"),
	key.WithHelp("enter", "inspect"),
)

var backKeys = key.NewBinding(
	key.WithKeys("esc", "backspace"),
	key.WithHelp("esc", "back"),
)

var replayKeys = key.NewBinding(
	key.WithKeys("r"),
	key.WithHelp("r", "replay"),
)

var curlKeys = key.NewBinding(
	key.WithKeys("c"),
	key.WithHelp("c", "copy curl"),
)

var tabKeys = key.NewBinding(
	key.WithKeys("tab", "1", "2"),
	key.WithHelp("tab", "switch tab"),
)

// channelClosedMsg is sent when the event channel is closed
type channelClosedMsg struct{}

// tickMsg is sent every second to update the uptime display
type tickMsg time.Time

// forceRedrawMsg triggers a screen redraw to clean up SSH client stderr garbage
type forceRedrawMsg struct{}

// notificationClearMsg clears the notification after timeout
type notificationClearMsg struct{}

// ReplayResultMsg contains the result of a replay operation
type ReplayResultMsg struct {
	Success    bool
	StatusCode int
	DurationMs int64
	Error      string
}

// ClipboardMsg indicates clipboard operation result
type ClipboardMsg struct {
	Success bool
	Error   string
}

// Styles holds all TUI styles (created per-session for correct color profiles)
type Styles struct {
	Title    lipgloss.Style
	URL      lipgloss.Style
	Dim      lipgloss.Style
	Method   lipgloss.Style
	Success  lipgloss.Style
	Warn     lipgloss.Style
	Error    lipgloss.Style
	Selected lipgloss.Style
	Header   lipgloss.Style
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
		Selected: renderer.NewStyle().
			Background(lipgloss.Color("237")),
		Header: renderer.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39")),
	}
}

// logEntry represents either a request or an error in the log
type logEntry struct {
	request *events.RequestEvent
	err     *events.ErrorEvent
}

// TunnelModel is the main TUI for an active tunnel
type TunnelModel struct {
	tunnel        *tunnel.Tunnel
	tunnelURL     string
	eventCh       <-chan events.Event
	cleanup       func()
	store         *store.RequestStore
	replayer      Replayer
	entries       []logEntry
	requestCount  int
	errorCount    int
	startTime     time.Time
	width         int
	height        int
	quitting      bool
	styles        Styles
	viewport      viewport.Model
	ready         bool
	redrawPending bool // debounce multiple rapid error redraws

	// View state
	viewMode       ViewMode
	selectedIdx    int
	detailRecord   *events.RequestRecord
	detailViewport viewport.Model
	detailTab      DetailTab

	// Notification
	notification       string
	notificationExpiry time.Time
}

// Replayer is an interface for replaying requests
type Replayer interface {
	Replay(tunnelURL string, req *events.CapturedRequest) (statusCode int, durationMs int64, err error)
}

func NewTunnelModel(
	tun *tunnel.Tunnel,
	tunnelURL string,
	eventCh <-chan events.Event,
	cleanup func(),
	styles Styles,
	requestStore *store.RequestStore,
	replayer Replayer,
) *TunnelModel {
	return &TunnelModel{
		tunnel:    tun,
		tunnelURL: tunnelURL,
		eventCh:   eventCh,
		cleanup:   cleanup,
		store:     requestStore,
		replayer:  replayer,
		entries:   make([]logEntry, 0),
		startTime: time.Now(),
		styles:    styles,
		viewMode:  ViewList,
	}
}

func (m *TunnelModel) Init() tea.Cmd {
	return tea.Batch(m.waitForEvent(), m.tickCmd())
}

func (m *TunnelModel) tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *TunnelModel) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.eventCh
		if !ok {
			return channelClosedMsg{}
		}
		// Return the concrete type for type switching in Update
		switch e := event.(type) {
		case events.RequestEvent:
			return e
		case events.ErrorEvent:
			return e
		default:
			return event
		}
	}
}

const headerHeight = 7 // title + blank + url + blank + stats + blank + footer buffer

func (m *TunnelModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle keys based on current view mode
		switch m.viewMode {
		case ViewList:
			return m.updateListView(msg)
		case ViewDetail:
			return m.updateDetailView(msg)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		viewportHeight := m.height - headerHeight
		if viewportHeight < 1 {
			viewportHeight = 1
		}
		if !m.ready {
			m.viewport = viewport.New(m.width, viewportHeight)
			m.viewport.SetContent(m.renderEntries())
			m.detailViewport = viewport.New(m.width, viewportHeight)
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = viewportHeight
			m.detailViewport.Width = m.width
			m.detailViewport.Height = viewportHeight
		}

	case channelClosedMsg:
		m.quitting = true
		return m, tea.Quit

	case events.RequestEvent:
		m.requestCount++
		// Adjust selectedIdx since we prepend new entries
		if len(m.entries) > 0 {
			m.selectedIdx++
		}
		m.entries = append([]logEntry{{request: &msg}}, m.entries...)
		if m.ready {
			m.viewport.SetContent(m.renderEntries())
		}
		return m, m.waitForEvent()

	case events.ErrorEvent:
		m.errorCount++
		// Adjust selectedIdx since we prepend new entries
		if len(m.entries) > 0 {
			m.selectedIdx++
		}
		m.entries = append([]logEntry{{err: &msg}}, m.entries...)
		if m.ready {
			m.viewport.SetContent(m.renderEntries())
		}
		// Schedule a delayed redraw to clean up OpenSSH's stderr garbage
		if !m.redrawPending {
			m.redrawPending = true
			return m, tea.Batch(
				m.waitForEvent(),
				tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
					return forceRedrawMsg{}
				}),
			)
		}
		return m, m.waitForEvent()

	case forceRedrawMsg:
		m.redrawPending = false
		return m, tea.Sequence(tea.ExitAltScreen, tea.EnterAltScreen)

	case tickMsg:
		// Clear expired notifications
		if m.notification != "" && time.Now().After(m.notificationExpiry) {
			m.notification = ""
		}
		return m, m.tickCmd()

	case notificationClearMsg:
		m.notification = ""

	case ReplayResultMsg:
		if msg.Success {
			m.notification = fmt.Sprintf("Replayed: %d", msg.StatusCode)
		} else {
			m.notification = fmt.Sprintf("Replay failed: %s", msg.Error)
		}
		m.notificationExpiry = time.Now().Add(1500 * time.Millisecond)

	case ClipboardMsg:
		if msg.Success {
			m.notification = "Copied to clipboard"
		} else {
			m.notification = fmt.Sprintf("Copy failed: %s", msg.Error)
		}
		m.notificationExpiry = time.Now().Add(1500 * time.Millisecond)
	}

	// Update the appropriate viewport based on view mode
	if m.ready {
		var cmd tea.Cmd
		switch m.viewMode {
		case ViewList:
			m.viewport, cmd = m.viewport.Update(msg)
		case ViewDetail:
			m.detailViewport, cmd = m.detailViewport.Update(msg)
		}
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *TunnelModel) updateListView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, quitKeys):
		m.quitting = true
		m.cleanup()
		return m, tea.Quit

	case key.Matches(msg, redrawKeys):
		return m, tea.Sequence(tea.ExitAltScreen, tea.EnterAltScreen)

	case key.Matches(msg, upKeys):
		if m.selectedIdx > 0 {
			m.selectedIdx--
			m.viewport.SetContent(m.renderEntries())
		}

	case key.Matches(msg, downKeys):
		if m.selectedIdx < len(m.entries)-1 {
			m.selectedIdx++
			m.viewport.SetContent(m.renderEntries())
		}

	case key.Matches(msg, enterKeys):
		if len(m.entries) > 0 && m.entries[m.selectedIdx].request != nil {
			recordID := m.entries[m.selectedIdx].request.RecordID
			if record, ok := m.store.Get(m.tunnel.ID, recordID); ok {
				m.detailRecord = record
				m.viewMode = ViewDetail
				m.detailViewport.SetContent(m.renderDetail())
				m.detailViewport.GotoTop()
			}
		}

	case key.Matches(msg, replayKeys):
		return m.triggerReplay()
	}

	return m, nil
}

func (m *TunnelModel) updateDetailView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, quitKeys):
		m.quitting = true
		m.cleanup()
		return m, tea.Quit

	case key.Matches(msg, backKeys):
		m.viewMode = ViewList
		m.detailRecord = nil
		return m, nil

	case key.Matches(msg, replayKeys):
		return m.triggerReplay()

	case key.Matches(msg, curlKeys):
		return m.copyAsCurl()

	case key.Matches(msg, tabKeys):
		// Switch tabs
		if msg.String() == "1" {
			m.detailTab = TabRequest
		} else if msg.String() == "2" {
			m.detailTab = TabResponse
		} else {
			// Tab key toggles
			if m.detailTab == TabRequest {
				m.detailTab = TabResponse
			} else {
				m.detailTab = TabRequest
			}
		}
		m.detailViewport.SetContent(m.renderDetail())
		m.detailViewport.GotoTop()
		return m, nil
	}

	// Pass through to viewport for scrolling (arrows, pgup/pgdn, etc.)
	var cmd tea.Cmd
	m.detailViewport, cmd = m.detailViewport.Update(msg)
	return m, cmd
}

func (m *TunnelModel) triggerReplay() (tea.Model, tea.Cmd) {
	var record *events.RequestRecord

	if m.viewMode == ViewDetail && m.detailRecord != nil {
		record = m.detailRecord
	} else if m.viewMode == ViewList && len(m.entries) > 0 {
		if entry := m.entries[m.selectedIdx]; entry.request != nil {
			record, _ = m.store.Get(m.tunnel.ID, entry.request.RecordID)
		}
	}

	if record == nil || m.replayer == nil {
		return m, nil
	}

	// Create a command that performs the replay asynchronously
	req := record.Request
	tunnelURL := m.tunnelURL
	return m, func() tea.Msg {
		statusCode, durationMs, err := m.replayer.Replay(tunnelURL, &req)
		if err != nil {
			return ReplayResultMsg{Success: false, Error: err.Error()}
		}
		return ReplayResultMsg{Success: true, StatusCode: statusCode, DurationMs: durationMs}
	}
}

func (m *TunnelModel) copyAsCurl() (tea.Model, tea.Cmd) {
	if m.detailRecord == nil {
		return m, nil
	}

	curlCmd := ToCurlCommand(&m.detailRecord.Request, m.tunnelURL)
	return m, func() tea.Msg {
		if err := copyToClipboard(curlCmd); err != nil {
			return ClipboardMsg{Success: false, Error: err.Error()}
		}
		return ClipboardMsg{Success: true}
	}
}

func (m *TunnelModel) renderEntries() string {
	if len(m.entries) == 0 {
		return m.styles.Dim.Italic(true).Render("Waiting for requests...")
	}
	lines := make([]string, 0, len(m.entries))
	for i, e := range m.entries {
		isSelected := i == m.selectedIdx
		var line string
		if e.request != nil {
			line = m.formatRequest(*e.request, isSelected)
		} else if e.err != nil {
			line = m.formatError(*e.err, isSelected)
		}
		lines = append(lines, line)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m *TunnelModel) View() string {
	if m.quitting {
		return ""
	}

	switch m.viewMode {
	case ViewDetail:
		return m.viewDetail()
	default:
		return m.viewList()
	}
}

func (m *TunnelModel) viewList() string {
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

	var entriesView string
	if m.ready {
		entriesView = m.viewport.View()
	} else {
		entriesView = m.renderEntries()
	}

	footer := m.styles.Dim.Render("↑↓ navigate • enter inspect • r replay • q quit")
	if m.notification != "" {
		footer = m.styles.Success.Render(m.notification)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		entriesView,
		"",
		footer,
	)
}

func (m *TunnelModel) viewDetail() string {
	// Build tab bar
	var reqTab, respTab string
	if m.detailTab == TabRequest {
		reqTab = m.styles.Selected.Render(" [1] Request ")
		respTab = m.styles.Dim.Render(" [2] Response ")
	} else {
		reqTab = m.styles.Dim.Render(" [1] Request ")
		respTab = m.styles.Selected.Render(" [2] Response ")
	}
	tabs := lipgloss.JoinHorizontal(lipgloss.Top, reqTab, respTab)

	// Add status summary
	var status string
	if m.detailRecord != nil && m.detailRecord.Response != nil {
		status = fmt.Sprintf("  %s %s  %dms",
			m.detailRecord.Request.Method,
			m.detailRecord.Response.Status,
			m.detailRecord.DurationMs)
	} else if m.detailRecord != nil {
		status = fmt.Sprintf("  %s  %s", m.detailRecord.Request.Method, m.detailRecord.Error)
	}

	var content string
	if m.ready {
		content = m.detailViewport.View()
	} else {
		content = m.renderDetail()
	}

	footer := m.styles.Dim.Render("↑↓ scroll • r replay • c curl • esc back • q quit")
	if m.notification != "" {
		footer = m.styles.Success.Render(m.notification)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		tabs,
		m.styles.Dim.Render(status),
		"",
		content,
		"",
		footer,
	)
}

func (m *TunnelModel) formatRequest(r events.RequestEvent, isSelected bool) string {
	timeStr := r.Timestamp.Format("15:04:05")

	var statusStyle lipgloss.Style
	if r.StatusCode >= 400 {
		statusStyle = m.styles.Error
	} else if r.StatusCode >= 300 {
		statusStyle = m.styles.Warn
	} else {
		statusStyle = m.styles.Success
	}

	prefix := "  "
	if isSelected {
		prefix = "> "
	}

	line := fmt.Sprintf("%s%s %s %s %s %dms",
		prefix,
		m.styles.Dim.Render(timeStr),
		m.styles.Method.Render(r.Method),
		truncate(r.Path, 40),
		statusStyle.Render(fmt.Sprintf("%d", r.StatusCode)),
		r.DurationMs,
	)

	if isSelected {
		return m.styles.Selected.Render(line)
	}
	return line
}

func (m *TunnelModel) formatError(e events.ErrorEvent, isSelected bool) string {
	timeStr := e.Timestamp.Format("15:04:05")

	prefix := "  "
	if isSelected {
		prefix = "> "
	}

	line := fmt.Sprintf("%s%s %s %s %s %s",
		prefix,
		m.styles.Dim.Render(timeStr),
		m.styles.Method.Render(e.Method),
		truncate(e.Path, 40),
		m.styles.Error.Render("502"),
		m.styles.Dim.Render(e.Message),
	)

	if isSelected {
		return m.styles.Selected.Render(line)
	}
	return line
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
