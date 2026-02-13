// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

// Package main implements the Traefik ForwardAuth server that validates
// GitHub fine-grained PATs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andrewkroh/traefik-github-auth/internal/cache"
	"github.com/andrewkroh/traefik-github-auth/internal/github"
	"github.com/andrewkroh/traefik-github-auth/internal/handler"
	"github.com/andrewkroh/traefik-github-auth/internal/otelsetup"
	"github.com/andrewkroh/traefik-github-auth/internal/validator"
)

// version is set at build time via -ldflags "-X main.version=v1.0.0".
var version = "dev"

// Config holds the server configuration parsed from CLI flags.
type Config struct {
	// Org is the GitHub organization name to validate membership against.
	Org string

	// Listen is the HTTP listen address.
	Listen string

	// CacheTTL is the duration for which cached validation results are valid.
	CacheTTL time.Duration

	// CacheMaxSize is the maximum number of entries in the token cache.
	CacheMaxSize int

	// RejectClassicPATs controls whether classic PATs are rejected.
	RejectClassicPATs bool
}

// parseFlags parses CLI flags from the given arguments into a Config.
// It uses a custom flag.FlagSet so that tests can call it without
// affecting the global flag.CommandLine state.
func parseFlags(args []string) (*Config, error) {
	fs := flag.NewFlagSet("traefik-github-auth", flag.ContinueOnError)

	cfg := &Config{}

	fs.StringVar(&cfg.Org, "org", "", "GitHub organization name to validate membership against (required)")
	fs.StringVar(&cfg.Listen, "listen", ":8080", "HTTP listen address")
	fs.DurationVar(&cfg.CacheTTL, "cache-ttl", 5*time.Minute, "Cache TTL duration")
	fs.IntVar(&cfg.CacheMaxSize, "cache-max-size", 1000, "Maximum number of entries in the token cache")
	fs.BoolVar(&cfg.RejectClassicPATs, "reject-classic-pats", true, "Whether to reject classic PATs")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if err := cfg.validate(); err != nil {
		// Print usage to stderr when validation fails.
		fmt.Fprintf(fs.Output(), "Error: %v\n\n", err)
		fs.Usage()
		return nil, err
	}

	return cfg, nil
}

// validate checks that the Config has all required fields set and that
// values are within acceptable ranges.
func (c *Config) validate() error {
	if c.Org == "" {
		return errors.New("flag -org is required")
	}
	if c.CacheTTL < 0 {
		return fmt.Errorf("flag -cache-ttl must be non-negative, got %s", c.CacheTTL)
	}
	if c.CacheMaxSize <= 0 {
		return fmt.Errorf("flag -cache-max-size must be positive, got %d", c.CacheMaxSize)
	}
	return nil
}

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		os.Exit(1)
	}

	// Set up slog with trace context injection.
	logger := otelsetup.NewLogger(os.Stderr)
	slog.SetDefault(logger)

	// Set up OpenTelemetry.
	ctx := context.Background()
	otelShutdown, err := otelsetup.Setup(ctx, "traefik-github-auth", version)
	if err != nil {
		slog.Error("failed to set up OpenTelemetry", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := otelShutdown(shutdownCtx); err != nil {
			slog.Error("OpenTelemetry shutdown error", slog.String("error", err.Error()))
		}
	}()

	// Create GitHub client.
	var ghOpts []github.Option
	if baseURL := os.Getenv("GITHUB_API_BASE_URL"); baseURL != "" {
		ghOpts = append(ghOpts, github.WithBaseURL(baseURL))
	}
	ghOpts = append(ghOpts, github.WithLogger(logger))
	ghClient := github.NewHTTPClient(ghOpts...)

	// Create cache.
	tokenCache := cache.New(cfg.CacheTTL, cfg.CacheMaxSize)
	defer tokenCache.Stop()

	// Create validator.
	v := validator.New(ghClient, tokenCache, cfg.Org, cfg.RejectClassicPATs, logger)

	// Create handler.
	h := handler.New(v, logger)

	// Create HTTP server.
	mux := h.Routes()
	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: mux,
	}

	// Graceful shutdown: listen for SIGINT and SIGTERM.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start the server in a goroutine.
	go func() {
		slog.Info("server starting",
			slog.String("listen", cfg.Listen),
			slog.String("org", cfg.Org),
			slog.Duration("cache_ttl", cfg.CacheTTL),
			slog.Int("cache_max_size", cfg.CacheMaxSize),
			slog.Bool("reject_classic_pats", cfg.RejectClassicPATs),
			slog.String("version", version),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal.
	<-ctx.Done()
	slog.Info("shutting down server")

	// Give outstanding requests 10 seconds to complete.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", slog.String("error", err.Error()))
	}

	slog.Info("server stopped")
}
