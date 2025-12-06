package config

import (
	"os"
	"strconv"
)

type Config struct {
	SSHHost     string
	SSHPort     int
	HostKeyPath string
	HTTPPort    int
	BaseDomain  string
}

func Load() *Config {
	cfg := &Config{
		SSHHost:     "0.0.0.0",
		SSHPort:     2222,
		HostKeyPath: "./host_key",
		HTTPPort:    8080,
		BaseDomain:  "localhost",
	}

	if host := os.Getenv("SSH_HOST"); host != "" {
		cfg.SSHHost = host
	}
	if port := os.Getenv("SSH_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.SSHPort = p
		}
	}
	if port := os.Getenv("HTTP_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.HTTPPort = p
		}
	}
	if domain := os.Getenv("BASE_DOMAIN"); domain != "" {
		cfg.BaseDomain = domain
	}
	if keyPath := os.Getenv("HOST_KEY_PATH"); keyPath != "" {
		cfg.HostKeyPath = keyPath
	}

	return cfg
}
