package sshd

import (
	"context"
	"fmt"
	"log"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	"github.com/charmbracelet/wish/bubbletea"
	gossh "golang.org/x/crypto/ssh"

	"github.com/MorrisonWill/duct.sh/internal/events"
	"github.com/MorrisonWill/duct.sh/internal/tunnel"
	"github.com/MorrisonWill/duct.sh/internal/tui"
)

type Server struct {
	wishServer    *ssh.Server
	tunnelManager *tunnel.Manager
	eventHub      *events.Hub
	host          string
	port          int
	hostKeyPath   string
	baseDomain    string
}

func New(
	tunnelManager *tunnel.Manager,
	eventHub *events.Hub,
	host string,
	port int,
	hostKeyPath string,
	baseDomain string,
) *Server {
	return &Server{
		tunnelManager: tunnelManager,
		eventHub:      eventHub,
		host:          host,
		port:          port,
		hostKeyPath:   hostKeyPath,
		baseDomain:    baseDomain,
	}
}

func (s *Server) Start() error {
	srv, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%d", s.host, s.port)),
		wish.WithHostKeyPath(s.hostKeyPath),
		wish.WithPublicKeyAuth(s.publicKeyHandler),
		wish.WithMiddleware(
			s.tunnelMiddleware(),
			activeterm.Middleware(),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create SSH server: %w", err)
	}

	// Configure reverse port forwarding handlers
	srv.RequestHandlers = map[string]ssh.RequestHandler{
		"tcpip-forward":        s.handleTcpipForward,
		"cancel-tcpip-forward": s.handleCancelForward,
	}

	s.wishServer = srv

	log.Printf("SSH server listening on %s:%d", s.host, s.port)
	return srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.wishServer != nil {
		return s.wishServer.Shutdown(ctx)
	}
	return nil
}

// publicKeyHandler accepts all public keys.
// We identify users by their key fingerprint.
func (s *Server) publicKeyHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	fingerprint := gossh.FingerprintSHA256(key)
	ctx.SetValue("fingerprint", fingerprint)
	log.Printf("Auth: fingerprint=%s", fingerprint)
	return true
}

// handleTcpipForward handles ssh -R requests.
// This is called when the client requests remote port forwarding.
func (s *Server) handleTcpipForward(ctx ssh.Context, srv *ssh.Server, req *gossh.Request) (bool, []byte) {
	var payload struct {
		BindAddr string
		BindPort uint32
	}
	if err := gossh.Unmarshal(req.Payload, &payload); err != nil {
		log.Printf("Failed to parse tcpip-forward: %v", err)
		return false, nil
	}

	// Get the underlying connection
	conn := ctx.Value(ssh.ContextKeyConn).(*gossh.ServerConn)

	// Create the tunnel (allocates a port if BindPort is 0)
	tun, url, allocatedPort := s.tunnelManager.CreateTunnel(conn, payload.BindPort)

	// Store tunnel info in context for the session handler
	ctx.SetValue("tunnel", tun)
	ctx.SetValue("tunnel_url", url)

	log.Printf("Tunnel created: %s -> %s (port %d)", tun.Subdomain, url, allocatedPort)

	// Reply with the allocated port so client knows which port to use
	reply := struct{ Port uint32 }{Port: allocatedPort}
	return true, gossh.Marshal(&reply)
}

func (s *Server) handleCancelForward(ctx ssh.Context, srv *ssh.Server, req *gossh.Request) (bool, []byte) {
	return true, nil
}

// tunnelMiddleware runs the TUI or shows usage based on whether -R was used
func (s *Server) tunnelMiddleware() wish.Middleware {
	teaHandler := func(sshSession ssh.Session) (tea.Model, []tea.ProgramOption) {
		tun, hasTunnel := sshSession.Context().Value("tunnel").(*tunnel.Tunnel)

		if !hasTunnel || tun == nil {
			// No tunnel - show usage and exit
			return tui.NewUsageModel(s.baseDomain), nil
		}

		tunnelURL := sshSession.Context().Value("tunnel_url").(string)

		// Subscribe to events for this tunnel
		eventCh := s.eventHub.Subscribe(tun.ID)

		// Create cleanup function
		cleanup := func() {
			s.eventHub.Unsubscribe(tun.ID)
			s.tunnelManager.Close(tun.ID)
			log.Printf("Tunnel closed: %s", tun.Subdomain)
		}

		// Create renderer-based styles for correct color profile detection
		renderer := bubbletea.MakeRenderer(sshSession)
		styles := tui.NewStyles(renderer)

		return tui.NewTunnelModel(tun, tunnelURL, eventCh, cleanup, styles), []tea.ProgramOption{tea.WithAltScreen()}
	}

	return bubbletea.Middleware(teaHandler)
}
