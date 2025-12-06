package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MorrisonWill/duct.sh/internal/config"
	"github.com/MorrisonWill/duct.sh/internal/events"
	"github.com/MorrisonWill/duct.sh/internal/proxy"
	"github.com/MorrisonWill/duct.sh/internal/sshd"
	"github.com/MorrisonWill/duct.sh/internal/tunnel"
)

func main() {
	cfg := config.Load()

	eventHub := events.NewHub(100)
	tunnelManager := tunnel.NewManager(cfg.BaseDomain, cfg.HTTPPort, cfg.UseHTTPS)

	// Start HTTP proxy
	httpProxy := proxy.New(tunnelManager, eventHub, cfg.BaseDomain, cfg.HTTPPort)
	go func() {
		if err := httpProxy.Start(); err != nil {
			log.Fatalf("HTTP proxy failed: %v", err)
		}
	}()

	// Start SSH server
	sshServer := sshd.New(
		tunnelManager,
		eventHub,
		cfg.SSHHost,
		cfg.SSHPort,
		cfg.HostKeyPath,
		cfg.BaseDomain,
	)
	go func() {
		if err := sshServer.Start(); err != nil {
			log.Fatalf("SSH server failed: %v", err)
		}
	}()

	log.Printf("Duct started - SSH :%d, HTTP :%d, domain: *.%s",
		cfg.SSHPort, cfg.HTTPPort, cfg.BaseDomain)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sshServer.Shutdown(ctx)
	httpProxy.Shutdown(ctx)

	log.Println("Shutdown complete")
}
